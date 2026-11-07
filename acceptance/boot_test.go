package acceptance_test

import (
	"os"
	"path/filepath"
	"testing"
)

func readOnly(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

func TestNonWritableVolumeFailsAtStartupBeforeContactingCOROS(t *testing.T) {
	for _, volume := range []string{"WATCH_DIR", "STATE_DIR"} {
		for _, command := range []string{"sync-once", "run"} {
			t.Run(volume+"/"+command, func(t *testing.T) {
				w := newWorld(t, 1, 1)
				w.mustRun("login")
				w.env["POLL_INTERVAL_TESTING"] = "200ms"
				w.env["HTTP_ADDR"] = "127.0.0.1:0"
				sent := len(w.fake.Requests())
				readOnly(t, w.env[volume])

				r := w.run(command)

				if r.code == 0 {
					t.Fatal("expected a non-zero exit")
				}
				assertContains(t, r.stderr, w.env[volume], "not writable")
				if n := len(w.fake.Requests()); n != sent {
					t.Fatalf("%d requests reached COROS despite a non-writable volume", n-sent)
				}
			})
		}
	}
}

func TestStalePartFilesFromACrashAreRemovedAtBoot(t *testing.T) {
	for _, command := range []string{"sync-once", "run"} {
		t.Run(command, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.mustRun("login")
			stale := ".coros_" + labelID(0) + ".fit.part"
			unrelated := []string{".coros_notes.txt", "other.fit.part", "coros_" + labelID(1) + ".fit"}
			for _, name := range append([]string{stale}, unrelated...) {
				if err := os.WriteFile(filepath.Join(w.watch, name), []byte("half written"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			var logs string
			if command == "run" {
				d := w.startDaemon()
				eventually(t, "a cycle completes", func() bool { return d.status().LastCycle != nil })
				logs = d.errOut()
			} else {
				logs = w.mustRun(command).stderr
			}

			files := w.watchFiles()
			if _, ok := files[stale]; ok {
				t.Fatalf("stale %s is still in the watch folder", stale)
			}
			for _, name := range unrelated {
				if _, ok := files[name]; !ok {
					t.Errorf("%s was removed, but it is not a connector .part file", name)
				}
			}
			assertContains(t, logs, stale)
		})
	}
}
