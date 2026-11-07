package acceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

const (
	email    = "runner@example.com"
	password = "correct horse"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dreeve-coros-connector-acceptance")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "dreeve-coros-connector")
	build := exec.Command("go", "build", "-o", binary, "../cmd/dreeve-coros-connector")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build binary:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type world struct {
	t      *testing.T
	fake   *fakecoros.Server
	watch  string
	state  string
	tokens string
	env    map[string]string
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newWorld(t *testing.T, regionID, homeRegionID int) *world {
	t.Helper()
	fake := fakecoros.New(t, fakecoros.Config{
		FixturesDir:  "../testdata/coros",
		Email:        email,
		Password:     password,
		RegionID:     regionID,
		HomeRegionID: homeRegionID,
	})
	w := &world{t: t, fake: fake, watch: t.TempDir(), state: t.TempDir(), tokens: t.TempDir()}
	w.env = map[string]string{
		"COROS_API_HOSTS":        fake.HostsEnv(),
		"COROS_EMAIL":            email,
		"COROS_PASSWORD":         password,
		"WATCH_DIR":              w.watch,
		"STATE_DIR":              w.state,
		"TOKENS_DIR":             w.tokens,
		"TZ":                     "UTC",
		"DOWNLOAD_DELAY_TESTING": "10ms",
	}
	return w
}

func (w *world) command(args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range w.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func (w *world) run(args ...string) result {
	w.t.Helper()
	cmd := w.command(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		w.t.Fatalf("start %v: %v", args, err)
	}
	timeout := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	err := cmd.Wait()
	if !timeout.Stop() {
		w.t.Fatalf("%v did not exit within 30s\nstdout: %s\nstderr: %s", args, stdout.String(), stderr.String())
	}
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		w.t.Fatalf("run %v: %v", args, err)
	}
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (w *world) mustRun(args ...string) result {
	w.t.Helper()
	r := w.run(args...)
	if r.code != 0 {
		w.t.Fatalf("%v exited %d\nstdout: %s\nstderr: %s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

func (w *world) addActivity(labelID string, daysAgo int, opts ...func(*fakecoros.Activity)) fakecoros.Activity {
	a := fakecoros.Activity{
		LabelID:   labelID,
		SportType: 100,
		Name:      "Run " + labelID,
		Start:     nowMinusDays(daysAgo),
		Content:   []byte("FIT content of " + labelID),
	}
	for _, opt := range opts {
		opt(&a)
	}
	w.fake.AddActivity(a)
	return a
}

func withoutFile(a *fakecoros.Activity) { a.NoFile = true }

func (w *world) watchFiles() map[string][]byte {
	w.t.Helper()
	entries, err := os.ReadDir(w.watch)
	if err != nil {
		w.t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(w.watch, e.Name()))
		if err != nil {
			w.t.Fatal(err)
		}
		files[e.Name()] = data
	}
	return files
}

func (w *world) clearWatch() {
	w.t.Helper()
	for name := range w.watchFiles() {
		if err := os.Remove(filepath.Join(w.watch, name)); err != nil {
			w.t.Fatal(err)
		}
	}
}

type ledgerDoc struct {
	Version int    `json:"version"`
	Since   string `json:"since"`
	Backoff *struct {
		Until time.Time `json:"until"`
		Count int       `json:"count"`
	} `json:"backoff"`
	Activities map[string]struct {
		Status     string `json:"status"`
		File       string `json:"file"`
		ResultCode string `json:"resultCode"`
		Attempts   int    `json:"attempts"`
		Error      string `json:"error"`
	} `json:"activities"`
}

func (w *world) ledgerPath() string { return filepath.Join(w.state, "ledger.json") }

func (w *world) ledger() ledgerDoc {
	w.t.Helper()
	data, err := os.ReadFile(w.ledgerPath())
	if err != nil {
		w.t.Fatal(err)
	}
	var l ledgerDoc
	if err := json.Unmarshal(data, &l); err != nil {
		w.t.Fatalf("ledger is not valid JSON: %v\n%s", err, data)
	}
	return l
}

func (w *world) tokenFiles() []string {
	w.t.Helper()
	entries, err := os.ReadDir(w.tokens)
	if err != nil {
		w.t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertContains(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("expected output to contain %q, got:\n%s", n, haystack)
		}
	}
}

func nowMinusDays(days int) time.Time {
	return time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
}
