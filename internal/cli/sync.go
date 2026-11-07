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

	today := startOfDay(time.Now())
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

	var delivered, noFile int
	for _, a := range activities {
		if l.Has(a.LabelID) {
			continue
		}
		if !safeLabelID.MatchString(a.LabelID) {
			return fmt.Errorf("COROS returned an unexpected activity id %q", a.LabelID)
		}
		entry, err := deliver(ctx, client, session, p.watch, a)
		if err != nil {
			return explainTokenError(err, tok)
		}
		l.Record(a.LabelID, entry)
		if err := l.Save(p.ledgerFile()); err != nil {
			return err
		}
		switch entry.Status {
		case ledger.Delivered:
			delivered++
			_, _ = fmt.Fprintf(stdout, "delivered %s (%s)\n", entry.File, a.Name)
		case ledger.NoFile:
			noFile++
			_, _ = fmt.Fprintf(stdout, "no file for activity %s (%s), COROS result %s\n", a.LabelID, a.Name, entry.ResultCode)
		}
	}

	l.LastSync = today.Format(time.DateOnly)
	if err := l.Save(p.ledgerFile()); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "sync-once: %d listed since %s, %d delivered, %d without file\n", len(activities), from.Format(time.DateOnly), delivered, noFile)
	return nil
}

func deliver(ctx context.Context, client *coros.Client, session *coros.Session, watchDir string, a coros.Activity) (ledger.Entry, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	fileURL, err := session.DownloadURL(ctx, a)
	var resultErr *coros.ResultError
	if errors.As(err, &resultErr) {
		return ledger.Entry{Status: ledger.NoFile, SportType: a.SportType, Name: a.Name, ResultCode: resultErr.Code, At: now}, nil
	}
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
	return ledger.Entry{Status: ledger.Delivered, SportType: a.SportType, Name: a.Name, File: name, At: now}, nil
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
	return fmt.Errorf("COROS rejected the saved token (result 1019, token age %s): it was revoked by a newer login, e.g. Training Hub or another connector, or it expired; run `login` again", age)
}
