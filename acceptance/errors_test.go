package acceptance_test

import (
	"os"
	"testing"
)

func TestLoginWithWrongCredentialsFailsAndSavesNothing(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["COROS_PASSWORD"] = "wrong"

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "email or password", "COROS_EMAIL", "COROS_PASSWORD")
	w.assertNothingSaved()
}

func TestLoginInTheWrongRegionFailsAndSavesNothing(t *testing.T) {
	w := newWorld(t, 1, 3)

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "region 1", "rejected")
	w.assertNothingSaved()
}

func TestSyncOnceWithRevokedTokenFailsAndAsksForLogin(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.mustRun("login")
	w.fake.RevokeTokens()

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "revoked", "login")
	if files := w.watchFiles(); len(files) != 0 {
		t.Fatalf("delivered with a revoked token: %v", keys(files))
	}
}

func TestSyncOnceBeforeLoginAsksForLogin(t *testing.T) {
	w := newWorld(t, 1, 1)

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "login")
}

func TestErrorsAreDistinct(t *testing.T) {
	badCreds := newWorld(t, 1, 1)
	badCreds.env["COROS_PASSWORD"] = "wrong"
	wrongRegion := newWorld(t, 1, 3)
	revoked := newWorld(t, 1, 1)
	revoked.mustRun("login")
	revoked.fake.RevokeTokens()

	messages := map[string]bool{
		badCreds.run("login").stderr:    true,
		wrongRegion.run("login").stderr: true,
		revoked.run("sync-once").stderr: true,
	}
	if len(messages) != 3 {
		t.Fatalf("expected three distinct error messages, got %v", messages)
	}
}

func TestCorruptLedgerAbortsAndIsLeftUntouched(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.mustRun("login")
	corrupt := []byte(`{"version":1,"since":"2026-10-08","activities":{"9`)
	if err := os.WriteFile(w.ledgerPath(), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "ledger")
	after, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("ledger was modified: %q", after)
	}
	if files := w.watchFiles(); len(files) != 0 {
		t.Fatalf("delivered despite a corrupt ledger: %v", keys(files))
	}
}

func TestUnknownCommandIsAUsageError(t *testing.T) {
	w := newWorld(t, 1, 1)

	r := w.run("frobnicate")

	if r.code != 2 {
		t.Fatalf("exit code = %d, want 2", r.code)
	}
	assertContains(t, r.stderr, "login", "sync-once")
}
