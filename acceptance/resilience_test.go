package acceptance_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func labelID(n int) string {
	return "9000000000000000" + strconv.Itoa(10+n)
}

func (w *world) requestsTo(e fakecoros.Endpoint) []fakecoros.Request {
	var out []fakecoros.Request
	for _, r := range w.fake.Requests() {
		if r.Endpoint == e {
			out = append(out, r)
		}
	}
	return out
}

func (w *world) assertNoFileInWatch() {
	w.t.Helper()
	if files := w.watchFiles(); len(files) != 0 {
		w.t.Fatalf("expected an empty watch folder, got %v", keys(files))
	}
}

func TestBackfillLargerThanTheCycleLimitIsDeliveredAcrossCycles(t *testing.T) {
	w := newWorld(t, 1, 1)
	for i := range 5 {
		w.addActivity(labelID(i), 10-i)
	}
	w.env["MAX_DOWNLOADS_PER_CYCLE"] = "2"
	w.mustRun("login")

	for run, want := range []int{2, 4, 5, 5} {
		before := len(w.requestsTo(fakecoros.Download))
		w.mustRun("sync-once")
		if n := len(w.requestsTo(fakecoros.Download)) - before; n > 2 {
			t.Fatalf("run %d sent %d download requests, want at most 2", run+1, n)
		}
		if got := len(w.watchFiles()); got != want {
			t.Fatalf("after run %d, %d files delivered, want %d", run+1, got, want)
		}
	}
	for i := range 5 {
		if n := w.fake.RequestsTo(fakecoros.Download, labelID(i)); n != 1 {
			t.Errorf("%s download requested %d times, want 1", labelID(i), n)
		}
	}
}

func TestConsecutiveDownloadsAreAtLeastTheDownloadDelayApart(t *testing.T) {
	w := newWorld(t, 1, 1)
	for i := range 3 {
		w.addActivity(labelID(i), 3-i)
	}
	delete(w.env, "DOWNLOAD_DELAY_TESTING")
	w.env["DOWNLOAD_DELAY_SECONDS"] = "1"
	w.mustRun("login")

	w.mustRun("sync-once")

	downloads := w.requestsTo(fakecoros.Download)
	if len(downloads) != 3 {
		t.Fatalf("expected 3 download requests, got %d", len(downloads))
	}
	for i := 1; i < len(downloads); i++ {
		if gap := downloads[i].At.Sub(downloads[i-1].At); gap < time.Second {
			t.Errorf("download %d started %v after the previous one, want at least 1s", i+1, gap)
		}
	}
}

func TestDownloadDelayBelowOneSecondIsRejected(t *testing.T) {
	w := newWorld(t, 1, 1)
	delete(w.env, "DOWNLOAD_DELAY_TESTING")
	w.env["DOWNLOAD_DELAY_SECONDS"] = "0"
	w.mustRun("login")

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "DOWNLOAD_DELAY_SECONDS")
}

func TestInvalidLimitsAreRejected(t *testing.T) {
	for _, name := range []string{"MAX_DOWNLOADS_PER_CYCLE", "MAX_BACKOFF_SECONDS", "MAX_ATTEMPTS"} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.mustRun("login")
			w.env[name] = "0"

			r := w.run("sync-once")

			if r.code == 0 {
				t.Fatal("expected a non-zero exit")
			}
			assertContains(t, r.stderr, name)
		})
	}
}

func TestThrottledSyncSendsNoRequestUntilTheBackoffElapses(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(labelID(0), 2)
	w.addActivity(labelID(1), 1)
	w.mustRun("login")
	w.fake.InjectFaults(fakecoros.Download, fakecoros.Throttle)

	before := time.Now()
	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit after a 429")
	}
	assertContains(t, r.stderr, "429")
	w.assertNoFileInWatch()
	b := w.ledger().Backoff
	if b == nil || b.Count != 1 {
		t.Fatalf("ledger backoff = %+v, want a first backoff", b)
	}
	if wait := b.Until.Sub(before); wait < 15*time.Minute || wait > 16*time.Minute {
		t.Fatalf("backoff lasts %v, want 15 minutes", wait)
	}
	if n := w.fake.RequestsTo(fakecoros.Download, labelID(1)); n != 0 {
		t.Fatalf("a download was requested after the 429")
	}

	sent := len(w.fake.Requests())
	r = w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit while backing off")
	}
	assertContains(t, r.stderr, "backing off until")
	if n := len(w.fake.Requests()); n != sent {
		t.Fatalf("%d requests reached COROS while backing off", n-sent)
	}
}

func TestBackoffGrowsAcrossConsecutive429sAndNeverExceedsTheCap(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(labelID(0), 1)
	w.env["MAX_BACKOFF_SECONDS"] = "2400"
	w.mustRun("login")

	var waits []time.Duration
	for range 3 {
		w.fake.InjectFaults(fakecoros.Query, fakecoros.Throttle)
		before := time.Now()
		w.run("sync-once")
		b := w.ledger().Backoff
		if b == nil {
			t.Fatal("no backoff recorded after a 429")
		}
		waits = append(waits, b.Until.Sub(before).Round(time.Minute))
		w.expireBackoff()
	}

	want := []time.Duration{15 * time.Minute, 30 * time.Minute, 40 * time.Minute}
	for i := range want {
		if waits[i] != want[i] {
			t.Errorf("backoff after 429 #%d = %v, want %v", i+1, waits[i], want[i])
		}
	}
}

func TestSyncResumesAndClearsTheBackoffOnceItElapses(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1)
	w.mustRun("login")
	w.fake.InjectFaults(fakecoros.File, fakecoros.Throttle)
	w.run("sync-once")
	w.expireBackoff()

	w.mustRun("sync-once")

	if files := w.watchFiles(); string(files["coros_"+a.LabelID+".fit"]) != string(a.Content) {
		t.Fatalf("activity was not delivered after the backoff, got %v", keys(files))
	}
	if b := w.ledger().Backoff; b != nil {
		t.Fatalf("backoff %+v kept after a successful sync", b)
	}
}

func (w *world) expireBackoff() {
	w.t.Helper()
	data, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		w.t.Fatal(err)
	}
	past := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	until := w.ledger().Backoff.Until.UTC().Format(time.RFC3339Nano)
	expired := strings.Replace(string(data), until, past, 1)
	if expired == string(data) {
		w.t.Fatalf("backoff until %s not found in ledger:\n%s", until, data)
	}
	if err := os.WriteFile(w.ledgerPath(), []byte(expired), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func TestTransientFailureIsRetriedAndEventuallyDelivered(t *testing.T) {
	cases := map[string]struct {
		endpoint fakecoros.Endpoint
		fault    fakecoros.Fault
	}{
		"server error on download": {fakecoros.Download, fakecoros.ServerError},
		"server error on file":     {fakecoros.File, fakecoros.ServerError},
		"truncated file":           {fakecoros.File, fakecoros.Truncate},
		"file timeout":             {fakecoros.File, fakecoros.Stall},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.env["HTTP_TIMEOUT_TESTING"] = "300ms"
			a := w.addActivity(labelID(0), 1)
			w.mustRun("login")
			w.fake.InjectFaults(c.endpoint, c.fault, c.fault)

			for attempt := 1; attempt <= 2; attempt++ {
				r := w.mustRun("sync-once")
				w.assertNoFileInWatch()
				e := w.ledger().Activities[a.LabelID]
				if e.Status != "pending" || e.Attempts != attempt || e.Error == "" {
					t.Fatalf("after attempt %d, ledger entry = %+v, want pending with the error", attempt, e)
				}
				assertContains(t, r.stderr, a.LabelID, "will retry")
			}

			w.mustRun("sync-once")

			if files := w.watchFiles(); len(files) != 1 || string(files["coros_"+a.LabelID+".fit"]) != string(a.Content) {
				t.Fatalf("activity was not delivered intact, got %v", keys(files))
			}
			if e := w.ledger().Activities[a.LabelID]; e.Status != "delivered" {
				t.Fatalf("ledger entry = %+v, want delivered", e)
			}
		})
	}
}

func TestActivityFailingMaxAttemptsIsReportedFailedAndNoLongerRetried(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1)
	w.env["MAX_ATTEMPTS"] = "2"
	w.mustRun("login")
	w.fake.InjectFaults(fakecoros.File, fakecoros.ServerError, fakecoros.ServerError, fakecoros.ServerError)

	w.mustRun("sync-once")
	w.mustRun("sync-once")

	e := w.ledger().Activities[a.LabelID]
	if e.Status != "failed" || e.Attempts != 2 || !strings.Contains(e.Error, "503") {
		t.Fatalf("ledger entry = %+v, want failed after 2 attempts with the HTTP 503", e)
	}

	w.mustRun("sync-once")

	if n := w.fake.RequestsTo(fakecoros.Download, a.LabelID); n != 2 {
		t.Fatalf("download requested %d times, want 2", n)
	}
	w.assertNoFileInWatch()
}

func TestUnexpectedDownloadResultBecomesNoFileAfterMaxAttempts(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1, withoutFile)
	w.env["MAX_ATTEMPTS"] = "2"
	w.mustRun("login")

	for range 2 {
		r := w.mustRun("sync-once")
		assertContains(t, r.stderr, a.LabelID, "9999")
	}

	if e := w.ledger().Activities[a.LabelID]; e.Status != "no-file" || e.ResultCode != "9999" || e.Attempts != 2 {
		t.Fatalf("ledger entry = %+v, want no-file with result 9999 after 2 attempts", e)
	}
	w.mustRun("sync-once")
	if n := w.fake.RequestsTo(fakecoros.Download, a.LabelID); n != 2 {
		t.Fatalf("download requested %d times, want 2", n)
	}
}

func TestProcessKilledMidDownloadLeavesNoFileAndTheNextRunDeliversIt(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1)
	w.mustRun("login")
	w.fake.InjectFaults(fakecoros.File, fakecoros.Stall)

	cmd := w.command("sync-once")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the file download to start", func() bool { return w.fake.RequestsTo(fakecoros.File, a.LabelID) == 1 })
	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	w.assertNoFileInWatch()

	w.mustRun("sync-once")

	if files := w.watchFiles(); len(files) != 1 || string(files["coros_"+a.LabelID+".fit"]) != string(a.Content) {
		t.Fatalf("activity was not delivered after the kill, got %v", keys(files))
	}
}

func TestPartFileLeftByAKilledDeliveryIsReplacedByTheNextRun(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1)
	w.mustRun("login")
	part := filepath.Join(w.watch, ".coros_"+a.LabelID+".fit.part")
	if err := os.WriteFile(part, a.Content[:3], 0o600); err != nil {
		t.Fatal(err)
	}

	w.mustRun("sync-once")

	if files := w.watchFiles(); len(files) != 1 || string(files["coros_"+a.LabelID+".fit"]) != string(a.Content) {
		t.Fatalf("expected only the complete file, got %v", keys(files))
	}
}

func TestMaxBackoffLongerThanAWeekIsRejected(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["MAX_BACKOFF_SECONDS"] = "604801"

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "MAX_BACKOFF_SECONDS", "604800")
}

func TestMaxBackoffOfExactlyAWeekIsAccepted(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["MAX_BACKOFF_SECONDS"] = "604800"

	w.mustRun("sync-once")
}
