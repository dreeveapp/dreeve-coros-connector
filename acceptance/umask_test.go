package acceptance_test

import (
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestUmaskSetsTheModeOfEveryFileWritten(t *testing.T) {
	for umask, want := range map[string]struct{ fit, ledger, token os.FileMode }{
		"":     {0o644, 0o644, 0o600},
		"022":  {0o644, 0o644, 0o600},
		"002":  {0o664, 0o664, 0o600},
		"0027": {0o640, 0o640, 0o600},
		"077":  {0o600, 0o600, 0o600},
	} {
		t.Run("UMASK="+umask, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.addActivity(labelID(0), 1)
			w.env["UMASK"] = umask

			w.mustRun("login")
			w.mustRun("sync-once")

			if got := mode(t, filepath.Join(w.watch, "coros_"+labelID(0)+".fit")); got != want.fit {
				t.Errorf("FIT file mode = %v, want %v", got, want.fit)
			}
			if got := mode(t, w.ledgerPath()); got != want.ledger {
				t.Errorf("ledger mode = %v, want %v", got, want.ledger)
			}
			if got := mode(t, filepath.Join(w.tokens, "token.json")); got != want.token {
				t.Errorf("token mode = %v, want %v", got, want.token)
			}
		})
	}
}

func TestInvalidUmaskFailsAtStartup(t *testing.T) {
	for _, umask := range []string{"rw-r--r--", "0888", "1022", "-022"} {
		t.Run(umask, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.mustRun("login")
			w.env["UMASK"] = umask

			r := w.run("sync-once")

			if r.code == 0 {
				t.Fatal("expected a non-zero exit")
			}
			assertContains(t, r.stderr, "UMASK", umask)
		})
	}
}
