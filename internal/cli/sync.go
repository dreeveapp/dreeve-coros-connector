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
	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
)

var safeLabelID = regexp.MustCompile(`^[0-9A-Za-z]+$`)

func syncOnce(ctx context.Context, getenv Env, stdout io.Writer) error {
	p := loadPaths(getenv)
	lookback, err := lookbackDays(getenv)
	if err != nil {
		return err
	}
	client, err := newClient(getenv)
	if err != nil {
		return err
	}
	l, err := ledger.Load(p.ledgerFile())
	if err != nil {
		return err
	}
	tok, err := loadToken(p.tokens)
	if err != nil {
		return err
	}

	zone, err := timeZone(getenv)
	if err != nil {
		return err
	}
	today := startOfDay(time.Now().In(zone))
	if l.Since == "" {
		since, err := resolveSince(envOr(getenv, "SINCE", "-30d"), today)
		if err != nil {
			return err
		}
		l.Since = since.Format(time.DateOnly)
	}
	from, err := windowStart(l, today, lookback)
	if err != nil {
		return err
	}

	session := client.Session(tok.Host, tok.Token)
	activities, err := session.ListActivities(ctx, from, today)
	if err != nil {
		return explainTokenError(err, tok)
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
		entry, err := deliver(ctx, client, session, p.watch, a)
		var resultErr *coros.ResultError
		if errors.As(err, &resultErr) {
			retry++
			if day := startOfDay(time.Unix(a.StartTime, 0).In(zone)); day.Before(completeUntil) {
				completeUntil = day
			}
			_, _ = fmt.Fprintf(stdout, "no file yet for activity %s (%s): COROS result %s %q, will retry next run\n", a.LabelID, a.Name, resultErr.Code, resultErr.Message)
			continue
		}
		if err != nil {
			return explainTokenError(err, tok)
		}
		l.Record(a.LabelID, entry)
		if err := l.Save(p.ledgerFile()); err != nil {
			return err
		}
		delivered++
		_, _ = fmt.Fprintf(stdout, "delivered %s (%s)\n", entry.File, a.Name)
	}

	l.LastSync = completeUntil.Format(time.DateOnly)
	if err := l.Save(p.ledgerFile()); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "sync-once: %d listed since %s, %d delivered, %d to retry\n", len(activities), from.Format(time.DateOnly), delivered, retry)
	return nil
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
		return ledger.Entry{}, fmt.Errorf("deliver activity %s: %w", a.LabelID, err)
	}
	return ledger.Entry{Status: ledger.Delivered, SportType: a.SportType, Name: a.Name, File: name, At: time.Now().UTC().Format(time.RFC3339)}, nil
}

func windowStart(l *ledger.Ledger, today time.Time, lookback int) (time.Time, error) {
	since, err := time.ParseInLocation(time.DateOnly, l.Since, today.Location())
	if err != nil {
		return time.Time{}, err
	}
	if l.LastSync == "" {
		return since, nil
	}
	lastSync, err := time.ParseInLocation(time.DateOnly, l.LastSync, today.Location())
	if err != nil {
		return time.Time{}, err
	}
	from := today.AddDate(0, 0, -lookback)
	if lastSync.Before(from) {
		from = lastSync
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
	return fmt.Errorf("COROS rejected the saved token (result %s, token age %s): it was revoked by a newer login, e.g. Training Hub or another connector, or it expired; run `login` again", coros.ResultInvalidToken, age)
}
