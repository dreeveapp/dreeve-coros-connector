package cli

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/atomicfile"
	"github.com/xballoy/dreeve-coros-connector/internal/coros"
	"github.com/xballoy/dreeve-coros-connector/internal/daemon"
	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
	"github.com/xballoy/dreeve-coros-connector/internal/logging"
	"github.com/xballoy/dreeve-coros-connector/internal/throttle"
)

var safeLabelID = regexp.MustCompile(`^[0-9A-Za-z]+$`)

type syncer struct {
	paths    paths
	lookback int
	limits   limits
	client   *coros.Client
	zone     *time.Location
	since    string
	fileMode os.FileMode
	dryRun   bool
	log      *slog.Logger
	secrets  *logging.Secrets
}

func newSyncer(getenv Env, a *app) (*syncer, error) {
	lookback, err := lookbackDays(getenv)
	if err != nil {
		return nil, err
	}
	lim, err := loadLimits(getenv)
	if err != nil {
		return nil, err
	}
	client, err := newClient(getenv)
	if err != nil {
		return nil, err
	}
	zone, err := timeZone(getenv)
	if err != nil {
		return nil, err
	}
	dryRun, err := boolEnv(getenv, "DRY_RUN")
	if err != nil {
		return nil, err
	}
	return &syncer{dryRun: dryRun, paths: loadPaths(getenv), lookback: lookback, limits: lim, client: client, zone: zone, since: envOr(getenv, "SINCE", "-30d"), fileMode: a.umask.apply(0o666), log: a.log, secrets: a.secrets}, nil
}

func syncOnce(ctx context.Context, getenv Env, a *app) error {
	s, err := newSyncer(getenv, a)
	if err != nil {
		return err
	}
	if err := s.boot(); err != nil {
		return err
	}
	_, err = s.cycle(ctx, "sync-once")
	return err
}

type throttledError struct {
	until time.Time
	err   error
}

func (e *throttledError) Error() string {
	until := e.until.Local().Format(time.RFC3339)
	if e.err == nil {
		return fmt.Sprintf("COROS throttled an earlier request (HTTP 429): backing off until %s, no request sent", until)
	}
	return fmt.Sprintf("%v: COROS is throttling requests, backing off until %s", e.err, until)
}

func (e *throttledError) Unwrap() error { return e.err }

func isThrottled(err error) bool {
	var httpErr *coros.HTTPError
	return errors.As(err, &httpErr) && httpErr.Status == http.StatusTooManyRequests
}

func (s *syncer) cycle(ctx context.Context, label string) (map[string]int, error) {
	l, err := ledger.Load(s.paths.ledgerFile())
	if err != nil {
		return nil, &fatalError{err}
	}
	tok, err := loadToken(s.paths.tokens)
	if err != nil {
		return statusCounts(l), err
	}
	s.secrets.Add(tok.Token)
	s.log.Debug("loaded token", "host", tok.Host, "region", tok.RegionID, "age", time.Since(tok.IssuedAt).Round(time.Minute).String())
	th := throttle.New(throttle.Config{Delay: s.limits.delay, BaseBackoff: baseBackoff, MaxBackoff: s.limits.maxBackoff}, l.Backoff)
	if until, ok := th.BackingOff(); ok {
		return statusCounts(l), &throttledError{until: until}
	}

	today := startOfDay(time.Now().In(s.zone))
	if l.Since == "" {
		since, err := resolveSince(s.since, today)
		if err != nil {
			return statusCounts(l), &fatalError{err}
		}
		l.Since = since.Format(time.DateOnly)
	}
	from, err := windowStart(l, today, s.lookback)
	if err != nil {
		return statusCounts(l), &fatalError{err}
	}

	backOff := func(cause error) (map[string]int, error) {
		until := th.Throttled()
		l.Backoff = th.Backoff()
		if err := s.save(l); err != nil {
			return statusCounts(l), &fatalError{err}
		}
		return statusCounts(l), &throttledError{until: until, err: cause}
	}

	session := s.client.Session(tok.Host, tok.Token)
	s.log.Debug("listing activities", "from", from.Format(time.DateOnly), "to", today.Format(time.DateOnly))
	activities, err := session.ListActivities(ctx, from, today)
	if isThrottled(err) {
		return backOff(err)
	}
	if err != nil {
		return statusCounts(l), explainTokenError(err, tok)
	}
	slices.SortStableFunc(activities, func(a, b coros.Activity) int { return cmp.Compare(a.StartTime, b.StartTime) })

	var delivered, wouldDeliver, retry, attempted, deferred int
	completeUntil := today
	keepInWindow := func(a coros.Activity) {
		if day := startOfDay(time.Unix(a.StartTime, 0).In(s.zone)).AddDate(0, 0, -1); day.Before(completeUntil) {
			completeUntil = day
		}
	}
	for _, a := range activities {
		if e, ok := l.Activities[a.LabelID]; ok && e.Status.Settled() {
			s.log.Debug("already settled", "labelId", a.LabelID, "status", string(e.Status))
			continue
		}
		if !safeLabelID.MatchString(a.LabelID) {
			s.log.Warn("skipped activity with an unexpected id", "labelId", a.LabelID, "name", a.Name)
			continue
		}
		if s.dryRun {
			wouldDeliver++
			s.log.Info("dry run: would deliver", "file", fitName(a.LabelID), "labelId", a.LabelID, "name", a.Name, "thisCycle", wouldDeliver <= s.limits.maxDownloads)
			continue
		}
		if attempted == s.limits.maxDownloads {
			keepInWindow(a)
			deferred++
			continue
		}
		attempted++
		if err := th.Pace(ctx); err != nil {
			return statusCounts(l), err
		}
		entry, err := deliver(ctx, s.client, session, s.paths.watch, s.fileMode, a)
		th.Downloaded()
		if err != nil && ctx.Err() != nil {
			return statusCounts(l), ctx.Err()
		}
		var fatal *fatalError
		switch {
		case err == nil:
			delivered++
			s.log.Info("delivered", "file", entry.File, "labelId", a.LabelID, "name", a.Name)
		case isThrottled(err):
			keepInWindow(a)
			l.CompleteUntil = earlier(l.CompleteUntil, completeUntil)
			return backOff(err)
		case errors.Is(err, coros.ErrInvalidToken):
			return statusCounts(l), explainTokenError(err, tok)
		case errors.As(err, &fatal):
			return statusCounts(l), err
		default:
			entry = s.failedAttempt(l.Activities[a.LabelID], a, err)
			if entry.Status == ledger.Pending {
				keepInWindow(a)
				retry++
			}
		}
		l.Record(a.LabelID, entry)
		if err := s.save(l); err != nil {
			return statusCounts(l), &fatalError{err}
		}
	}

	th.Recovered()
	l.Backoff = th.Backoff()
	l.CompleteUntil = completeUntil.Format(time.DateOnly)
	if err := s.save(l); err != nil {
		return statusCounts(l), &fatalError{err}
	}
	if s.dryRun {
		s.log.Info(label+" dry run finished, nothing written", "listed", len(activities), "since", from.Format(time.DateOnly), "wouldDeliver", wouldDeliver, "maxPerCycle", s.limits.maxDownloads)
		return statusCounts(l), nil
	}
	s.log.Info(label+" finished", "listed", len(activities), "since", from.Format(time.DateOnly), "delivered", delivered, "toRetry", retry, "leftForNextCycle", deferred)
	return statusCounts(l), nil
}

func (s *syncer) save(l *ledger.Ledger) error {
	if s.dryRun {
		return nil
	}
	return l.Save(s.paths.ledgerFile(), s.fileMode)
}

func fitName(labelID string) string { return "coros_" + labelID + ".fit" }

func earlier(stored string, candidate time.Time) string {
	if stored != "" {
		if t, err := time.ParseInLocation(time.DateOnly, stored, candidate.Location()); err == nil && t.Before(candidate) {
			return stored
		}
	}
	return candidate.Format(time.DateOnly)
}

func (s *syncer) failedAttempt(previous ledger.Entry, a coros.Activity, err error) ledger.Entry {
	entry := ledger.Entry{
		Status:    ledger.Pending,
		SportType: a.SportType,
		Name:      a.Name,
		Attempts:  previous.Attempts + 1,
		Error:     err.Error(),
		At:        time.Now().UTC().Format(time.RFC3339),
	}
	var resultErr *coros.ResultError
	isResult := errors.As(err, &resultErr)
	if isResult {
		entry.ResultCode = resultErr.Code
		entry.Error = resultErr.Message
	}
	what := err.Error()
	if isResult {
		what = fmt.Sprintf("COROS result %s %q", resultErr.Code, resultErr.Message)
	}
	if entry.Attempts < s.limits.maxAttempts {
		s.log.Warn("download failed, will retry next run", "labelId", a.LabelID, "name", a.Name, "attempt", entry.Attempts, "maxAttempts", s.limits.maxAttempts, "error", what)
		return entry
	}
	entry.Status = ledger.Failed
	if isResult {
		entry.Status = ledger.NoFile
	}
	s.log.Warn("giving up on activity", "labelId", a.LabelID, "name", a.Name, "attempts", entry.Attempts, "error", what, "status", string(entry.Status))
	return entry
}

func statusCounts(l *ledger.Ledger) map[string]int {
	counts := map[string]int{}
	for status, n := range l.Counts() {
		counts[string(status)] = n
	}
	return counts
}

func deliver(ctx context.Context, client *coros.Client, session *coros.Session, watchDir string, mode os.FileMode, a coros.Activity) (ledger.Entry, error) {
	fileURL, err := session.DownloadURL(ctx, a)
	if err != nil {
		return ledger.Entry{}, err
	}
	data, err := client.Fetch(ctx, fileURL)
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("download activity %s: %w", a.LabelID, err)
	}

	name := fitName(a.LabelID)
	if err := atomicfile.Write(watchDir, "."+name+".part", name, mode, bytes.NewReader(data)); err != nil {
		return ledger.Entry{}, &fatalError{fmt.Errorf("deliver activity %s: %w", a.LabelID, err)}
	}
	return ledger.Entry{Status: ledger.Delivered, SportType: a.SportType, Name: a.Name, File: name, At: time.Now().UTC().Format(time.RFC3339)}, nil
}

func windowStart(l *ledger.Ledger, today time.Time, lookback int) (time.Time, error) {
	since, err := time.ParseInLocation(time.DateOnly, l.Since, today.Location())
	if err != nil {
		return time.Time{}, err
	}
	if l.CompleteUntil == "" {
		return since, nil
	}
	completeUntil, err := time.ParseInLocation(time.DateOnly, l.CompleteUntil, today.Location())
	if err != nil {
		return time.Time{}, err
	}
	from := today.AddDate(0, 0, -lookback)
	if completeUntil.Before(from) {
		from = completeUntil
	}
	if from.Before(since) {
		from = since
	}
	return from, nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func explainTokenError(err error, tok storedToken) error {
	if !errors.Is(err, coros.ErrInvalidToken) {
		return err
	}
	age := time.Since(tok.IssuedAt).Round(time.Minute)
	return &tokenError{state: daemon.TokenRejected, err: fmt.Errorf("COROS rejected the saved token (result %s, token age %s): it was revoked by a newer login, e.g. Training Hub or another connector, or it expired; run `login` again", coros.ResultInvalidToken, age)}
}
