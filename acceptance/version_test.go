package acceptance_test

import (
	"strings"
	"testing"
)

func TestVersionPrintsTheVersionTheBinaryWasBuiltWith(t *testing.T) {
	w := newWorld(t, 1, 1)

	r := w.mustRun("version")

	if r.stdout != builtVersion+"\n" {
		t.Fatalf("version printed %q, want %q", r.stdout, builtVersion+"\n")
	}
}

func TestRunLogsItsVersionAtStartup(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")

	d := w.startDaemon()

	eventually(t, "the startup log line", func() bool {
		return strings.Contains(d.errOut(), "serving /healthz") && strings.Contains(d.errOut(), "version="+builtVersion)
	})
}
