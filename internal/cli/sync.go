package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/atomicfile"
	"github.com/xballoy/dreeve-coros-connector/internal/coros"
	"github.com/xballoy/dreeve-coros-connector/internal/daemon"
	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
)

var safeLabelID = regexp.MustCompile(`^[0-9A-Za-z]+$`)

type syncer struct {
	paths    paths
	lookback int
	client   *coros.Client
	zone     *time.Location
	since    string
}

func newSyncer(getenv Env) (*syncer, error) {
	lookback, err := lookbackDays(getenv)
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
	return &syncer{paths: loadPaths(getenv), lookback: lookback, client: client, zone: zone, since: envOr(getenv, "SINCE", "-30d")}, nil
}

func syncOnce(ctx context.Context, getenv Env, stdout, _ io.Writer) error {
	s, err := newSyncer(getenv)
	if err != nil {
		return err
	}
	_, err = s.cycle(ctx, "sync-once", stdout)
	return err
}

func (s *syncer) cycle(ctx context.Context, label string, stdout io.Writer) (map[string]int, error) {
	l, err := ledger.Load(s.paths.ledgerFile())
	if err != nil {
		return nil, &fatalError{err}
	}
	tok, err := loadToken(s.paths.tokens)
	if err != nil {
		return statusCounts(l), err
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

	session := s.client.Session(tok.Host, tok.Token)
	activities, err := session.ListActivities(ctx, from, today)
	if err != nil {
		return statusCounts(l), explainTokenError(err, tok)
	}
	slices.SortStableFunc(activities, func(a, b coros.Activity) int { return cmp.Compare(a.StartTime, b.StartTime) })

	var delivered, retry int
	completeUntil := today
	for _, a := range activities {
		if l.Has(a.LabelID) {
			continue
		}
		if !safeLabelID.MatchString(a.LabelID) {
			_, _ = fmt.Fprintf(stdout, "skipped activity with unexpected id %q (%s)\n", a.LabelID, a.Name)
			continue
		}
		entry, err := deliver(ctx, s.client, session, s.paths.watch, a)
		var resultErr *coros.ResultError
		if errors.As(err, &resultErr) {
			retry++
			if day := startOfDay(time.Unix(a.StartTime, 0).In(s.zone)).AddDate(0, 0, -1); day.Before(completeUntil) {
				completeUntil = day
			}
			_, _ = fmt.Fprintf(stdout, "no file yet for activity %s (%s): COROS result %s %q, will retry next run\n", a.LabelID, a.Name, resultErr.Code, resultErr.Message)
			continue
		}
		if err != nil {
			return statusCounts(l), explainTokenError(err, tok)
		}
		l.Record(a.LabelID, entry)
		if err := l.Save(s.paths.ledgerFile()); err != nil {
			return statusCounts(l), &fatalError{err}
		}
		delivered++
		_, _ = fmt.Fprintf(stdout, "delivered %s (%s)\n", entry.File, a.Name)
	}

	l.CompleteUntil = completeUntil.Format(time.DateOnly)
	if err := l.Save(s.paths.ledgerFile()); err != nil {
		return statusCounts(l), &fatalError{err}
	}
	_, _ = fmt.Fprintf(stdout, "%s: %d listed since %s, %d delivered, %d to retry\n", label, len(activities), from.Format(time.DateOnly), delivered, retry)
	return statusCounts(l), nil
}

func statusCounts(l *ledger.Ledger) map[string]int {
	counts := map[string]int{}
	for status, n := range l.Counts() {
		counts[string(status)] = n
	}
	return counts
}

func deliver(ctx context.Context, client *coros.Client, session *coros.Session, watchDir string, a coros.Activity) (ledger.Entry, error) {
	fileURL, err := session.DownloadURL(ctx, a)
	if err != nil {
		return ledger.Entry{}, err
	}
	body, err := client.Fetch(ctx, fileURL)
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("download activity %s: %w", a.LabelID, err)
	}
	defer func() { _ = body.Close() }()

	name := "coros_" + a.LabelID + ".fit"
	if err := atomicfile.Write(watchDir, "."+name+".part", name, 0o644, body); err != nil {
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
