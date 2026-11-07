package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func TestLoginPersistsTheTokenPrivately(t *testing.T) {
	w := newWorld(t, 3, 3)
	r := w.mustRun("login")
	assertContains(t, r.stdout, "region 3")

	path := filepath.Join(w.tokens, "token.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tok struct {
		Token    string `json:"token"`
		Host     string `json:"host"`
		RegionID int    `json:"regionId"`
		IssuedAt string `json:"issuedAt"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		t.Fatalf("token file is not JSON: %v", err)
	}
	if tok.Token == "" || tok.Host != w.fake.RegionURL(3) || tok.RegionID != 3 {
		t.Fatalf("unexpected token file %s", data)
	}
	if _, err := time.Parse(time.RFC3339, tok.IssuedAt); err != nil {
		t.Fatalf("issuedAt %q is not RFC3339: %v", tok.IssuedAt, err)
	}
	if strings.Contains(string(data), password) {
		t.Fatal("token file must not contain the password")
	}
}

func TestSyncOnceDeliversOneFitFilePerActivityInTheWindow(t *testing.T) {
	w := newWorld(t, 1, 1)
	recent := w.addActivity("900000000000000002", 1)
	older := w.addActivity("900000000000000003", 20)
	w.addActivity("900000000000000004", 45)
	w.mustRun("login")

	w.mustRun("sync-once")

	files := w.watchFiles()
	if len(files) != 2 {
		t.Fatalf("expected 2 delivered files, got %v", keys(files))
	}
	for _, a := range []struct {
		id      string
		content []byte
	}{{recent.LabelID, recent.Content}, {older.LabelID, older.Content}} {
		name := "coros_" + a.id + ".fit"
		if string(files[name]) != string(a.content) {
			t.Errorf("%s content = %q, want %q", name, files[name], a.content)
		}
		info, err := os.Stat(filepath.Join(w.watch, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %v, want 0644", name, info.Mode().Perm())
		}
	}

	l := w.ledger()
	if l.Version != 1 {
		t.Errorf("ledger version = %d", l.Version)
	}
	wantSince := time.Now().UTC().AddDate(0, 0, -30).Format(time.DateOnly)
	if l.Since != wantSince {
		t.Errorf("ledger since = %q, want %q", l.Since, wantSince)
	}
	for _, id := range []string{recent.LabelID, older.LabelID} {
		if e := l.Activities[id]; e.Status != "delivered" || e.File != "coros_"+id+".fit" {
			t.Errorf("ledger entry for %s = %+v", id, e)
		}
	}
	if _, ok := l.Activities["900000000000000004"]; ok {
		t.Error("activity older than SINCE was recorded")
	}
}

func TestSecondSyncOnceDeliversNothingNew(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.addActivity("900000000000000003", 3)
	w.mustRun("login")
	w.mustRun("sync-once")
	w.clearWatch()

	w.mustRun("sync-once")

	if files := w.watchFiles(); len(files) != 0 {
		t.Fatalf("second run delivered %v", keys(files))
	}
	for _, id := range []string{"900000000000000002", "900000000000000003"} {
		if n := w.fake.FileDownloads(id); n != 1 {
			t.Errorf("%s downloaded %d times, want 1", id, n)
		}
	}
}

func TestActivityAddedWithinLookbackIsDeliveredOnTheNextRun(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 10)
	w.mustRun("login")
	w.mustRun("sync-once")
	w.clearWatch()

	late := w.addActivity("900000000000000003", 5)
	w.mustRun("sync-once")

	files := w.watchFiles()
	if len(files) != 1 || string(files["coros_"+late.LabelID+".fit"]) != string(late.Content) {
		t.Fatalf("expected only the late activity, got %v", keys(files))
	}
}

func TestSinceIsResolvedOnceAndKept(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.mustRun("sync-once")
	first := w.ledger().Since

	w.env["SINCE"] = "2020-01-01"
	w.mustRun("sync-once")

	if got := w.ledger().Since; got != first {
		t.Fatalf("since changed from %q to %q", first, got)
	}
}

func TestAbsoluteSinceIsHonouredOnTheFirstRun(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 2)
	w.addActivity("900000000000000003", 10)
	w.env["SINCE"] = time.Now().UTC().AddDate(0, 0, -5).Format(time.DateOnly)
	w.mustRun("login")

	w.mustRun("sync-once")

	if files := w.watchFiles(); len(files) != 1 || files["coros_900000000000000002.fit"] == nil {
		t.Fatalf("expected only the activity after SINCE, got %v", keys(files))
	}
}

func TestUnexpectedDownloadResultIsReportedAndRetriedOnTheNextRun(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1, withoutFile)
	w.mustRun("login")

	r := w.mustRun("sync-once")

	assertContains(t, r.stdout, "900000000000000002", "9999")
	if files := w.watchFiles(); len(files) != 0 {
		t.Fatalf("expected nothing delivered, got %v", keys(files))
	}
	if e := w.ledger().Activities["900000000000000002"]; e.Status != "pending" || e.Attempts != 1 || e.ResultCode != "9999" {
		t.Fatalf("activity with an unexpected download result recorded as %+v, want pending after 1 attempt with result 9999", e)
	}

	available := w.addActivity("900000000000000002", 1)
	w.mustRun("sync-once")

	if files := w.watchFiles(); string(files["coros_900000000000000002.fit"]) != string(available.Content) {
		t.Fatalf("activity was not retried, got %v", keys(files))
	}
}

func TestSyncOnceNeverUsesThePassword(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.mustRun("login")
	delete(w.env, "COROS_EMAIL")
	delete(w.env, "COROS_PASSWORD")

	w.mustRun("sync-once")

	if len(w.watchFiles()) != 1 {
		t.Fatal("expected the activity to be delivered without credentials")
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestNextRunCoversTheGapSinceTheLastCompletedSync(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.mustRun("sync-once")
	data, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["completeUntil"] = time.Now().UTC().AddDate(0, 0, -20).Format(time.DateOnly)
	data, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ledgerPath(), data, 0o644); err != nil {
		t.Fatal(err)
	}
	missed := w.addActivity("900000000000000002", 15)

	w.mustRun("sync-once")

	if files := w.watchFiles(); string(files["coros_"+missed.LabelID+".fit"]) != string(missed.Content) {
		t.Fatalf("activity from the downtime gap was not delivered, got %v", keys(files))
	}
}

func TestTimeZoneDefaultsToUTC(t *testing.T) {
	w := newWorld(t, 1, 1)
	delete(w.env, "TZ")
	w.mustRun("login")

	w.mustRun("sync-once")

	if want := time.Now().UTC().AddDate(0, 0, -30).Format(time.DateOnly); w.ledger().Since != want {
		t.Fatalf("since = %q, want %q", w.ledger().Since, want)
	}
}

func TestTimeZoneDecidesToday(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["TZ"] = "Pacific/Kiritimati"
	zone, err := time.LoadLocation("Pacific/Kiritimati")
	if err != nil {
		t.Fatal(err)
	}
	w.mustRun("login")

	w.mustRun("sync-once")

	if want := time.Now().In(zone).AddDate(0, 0, -30).Format(time.DateOnly); w.ledger().Since != want {
		t.Fatalf("since = %q, want %q", w.ledger().Since, want)
	}
}

func TestUnknownTimeZoneFails(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["TZ"] = "Mars/Olympus_Mons"

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "TZ", "Mars/Olympus_Mons")
}

func TestActivityAwaitingRetryStaysInTheWindowBeyondTheLookback(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 15, withoutFile)
	w.mustRun("login")
	w.mustRun("sync-once")

	available := w.addActivity("900000000000000002", 15)
	w.mustRun("sync-once")

	if files := w.watchFiles(); string(files["coros_900000000000000002.fit"]) != string(available.Content) {
		t.Fatalf("activity awaiting a retry fell out of the window, got %v", keys(files))
	}
}

func TestActivityAwaitingRetryIsKeptWhenTheTimeZoneIsAheadOfCOROS(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["TZ"] = "Pacific/Kiritimati"
	y, m, d := time.Now().UTC().AddDate(0, 0, -15).Date()
	lateEvening := time.Date(y, m, d, 20, 0, 0, 0, time.UTC)
	w.addActivity("900000000000000002", 0, withoutFile, func(a *fakecoros.Activity) { a.Start = lateEvening })
	w.mustRun("login")
	w.mustRun("sync-once")

	w.addActivity("900000000000000002", 0, func(a *fakecoros.Activity) { a.Start = lateEvening })
	w.mustRun("sync-once")

	if files := w.watchFiles(); files["coros_900000000000000002.fit"] == nil {
		t.Fatalf("activity awaiting a retry fell out of the window, got %v", keys(files))
	}
}

func TestSinceIsIgnoredOnceStoredEvenWhenInvalid(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.mustRun("sync-once")

	w.env["SINCE"] = "last month"

	w.mustRun("sync-once")
}
