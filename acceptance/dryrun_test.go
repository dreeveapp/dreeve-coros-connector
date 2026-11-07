package acceptance_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func TestDryRunReportsWhatWouldBeDeliveredAndWritesNothing(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(labelID(0), 3)
	w.mustRun("login")
	w.mustRun("sync-once")
	w.addActivity(labelID(1), 2)
	w.addActivity(labelID(2), 1, withoutFile)
	stale := filepath.Join(w.watch, ".coros_"+labelID(3)+".fit.part")
	if err := os.WriteFile(stale, []byte("half written"), 0o644); err != nil {
		t.Fatal(err)
	}
	watchBefore := w.watchFiles()
	ledgerBefore, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	w.env["DRY_RUN"] = "true"

	r := w.mustRun("sync-once")

	assertContains(t, r.stderr, "coros_"+labelID(1)+".fit", "coros_"+labelID(2)+".fit", "dry run")
	if !reflect.DeepEqual(w.watchFiles(), watchBefore) {
		t.Errorf("dry run changed the watch folder: %v, was %v", keys(w.watchFiles()), keys(watchBefore))
	}
	if after, _ := os.ReadFile(w.ledgerPath()); string(after) != string(ledgerBefore) {
		t.Errorf("dry run changed the ledger:\n%s\nwas:\n%s", after, ledgerBefore)
	}
	for _, id := range []string{labelID(1), labelID(2)} {
		if n := w.fake.RequestsTo(fakecoros.Download, id); n != 0 {
			t.Errorf("dry run requested the download of %s %d times", id, n)
		}
	}
}

func TestDryRunWorksOnReadOnlyVolumesAndCreatesNoLedger(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(labelID(0), 1)
	w.mustRun("login")
	readOnly(t, w.watch)
	readOnly(t, w.state)
	w.env["DRY_RUN"] = "true"

	r := w.mustRun("sync-once")

	assertContains(t, r.stderr, "coros_"+labelID(0)+".fit")
	if len(w.watchFiles()) != 0 {
		t.Errorf("dry run wrote to the watch folder: %v", keys(w.watchFiles()))
	}
	if _, err := os.Stat(w.ledgerPath()); !os.IsNotExist(err) {
		t.Errorf("dry run created a ledger: %v", err)
	}
}

func TestDryRunDoesNotPersistABackoff(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.mustRun("sync-once")
	ledgerBefore, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	w.fake.InjectFaults(fakecoros.Query, fakecoros.Throttle)
	w.env["DRY_RUN"] = "true"

	r := w.run("sync-once")

	assertContains(t, r.stderr, "429")
	if after, _ := os.ReadFile(w.ledgerPath()); string(after) != string(ledgerBefore) {
		t.Errorf("dry run changed the ledger:\n%s\nwas:\n%s", after, ledgerBefore)
	}
}

func TestInvalidDryRunFailsAtStartup(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["DRY_RUN"] = "maybe"

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "DRY_RUN", "maybe")
}
