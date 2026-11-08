package acceptance_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

type daemonProc struct {
	t      *testing.T
	cmd    *exec.Cmd
	addr   string
	mu     sync.Mutex
	stdout strings.Builder
	stderr strings.Builder
	exited chan struct{}
	code   int
}

var listening = regexp.MustCompile(`serving /healthz and /status.*?\baddr"?[=:]"?([^\s",]+)`)

func (w *world) startDaemon() *daemonProc {
	w.t.Helper()
	if _, ok := w.env["POLL_INTERVAL_TESTING"]; !ok {
		w.env["POLL_INTERVAL_TESTING"] = "200ms"
	}
	if _, ok := w.env["HTTP_ADDR"]; !ok {
		w.env["HTTP_ADDR"] = "127.0.0.1:0"
	}
	d := &daemonProc{t: w.t, cmd: w.command("run"), exited: make(chan struct{})}
	stdout, err := d.cmd.StdoutPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	stderr, err := d.cmd.StderrPipe()
	if err != nil {
		w.t.Fatal(err)
	}
	if err := d.cmd.Start(); err != nil {
		w.t.Fatal(err)
	}
	addr := make(chan string, 1)
	var copies sync.WaitGroup
	copies.Go(func() { d.collect(stdout, &d.stdout, nil) })
	copies.Go(func() { d.collect(stderr, &d.stderr, addr) })
	go func() {
		copies.Wait()
		err := d.cmd.Wait()
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			d.code = exitErr.ExitCode()
		}
		close(d.exited)
	}()
	w.t.Cleanup(func() {
		_ = d.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-d.exited:
		case <-time.After(10 * time.Second):
			_ = d.cmd.Process.Kill()
			w.t.Error("daemon did not stop on SIGTERM")
		}
	})
	select {
	case d.addr = <-addr:
	case <-d.exited:
		w.t.Fatalf("daemon exited %d before serving\nstdout: %s\nstderr: %s", d.code, d.out(), d.errOut())
	case <-time.After(10 * time.Second):
		w.t.Fatalf("daemon never reported its address\nstderr: %s", d.errOut())
	}
	w.env["HTTP_ADDR"] = d.addr
	return d
}

func (d *daemonProc) collect(r io.Reader, into *strings.Builder, addr chan<- string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		d.mu.Lock()
		into.WriteString(line + "\n")
		d.mu.Unlock()
		if m := listening.FindStringSubmatch(line); m != nil && addr != nil {
			addr <- m[1]
			addr = nil
		}
	}
}

func (d *daemonProc) out() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stdout.String()
}

func (d *daemonProc) errOut() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stderr.String()
}

func (d *daemonProc) alive() bool {
	select {
	case <-d.exited:
		return false
	default:
		return true
	}
}

func (d *daemonProc) healthz() int {
	d.t.Helper()
	resp, err := http.Get("http://" + d.addr + "/healthz")
	if err != nil {
		d.t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

type statusDoc struct {
	Healthy   bool           `json:"healthy"`
	Token     string         `json:"token"`
	Counts    map[string]int `json:"counts"`
	LastCycle *struct {
		StartedAt  time.Time `json:"startedAt"`
		FinishedAt time.Time `json:"finishedAt"`
		Error      string    `json:"error"`
	} `json:"lastCycle"`
}

func (d *daemonProc) status() statusDoc {
	d.t.Helper()
	resp, err := http.Get("http://" + d.addr + "/status")
	if err != nil {
		d.t.Fatalf("GET /status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var s statusDoc
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		d.t.Fatalf("/status is not JSON: %v", err)
	}
	return s
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (w *world) delivered(labelID string) func() bool {
	return func() bool {
		_, err := os.Stat(w.watch + "/coros_" + labelID + ".fit")
		return err == nil
	}
}

func TestRunDeliversNewActivitiesOnEachCycleWithoutRestart(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.mustRun("login")
	d := w.startDaemon()
	eventually(t, "the first activity", w.delivered("900000000000000002"))

	w.addActivity("900000000000000003", 0)

	eventually(t, "the activity added while running", w.delivered("900000000000000003"))
	if !d.alive() {
		t.Fatal("daemon exited")
	}
}

func TestRunReportsLedgerCountsAndTokenState(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.addActivity("900000000000000003", 2)
	w.mustRun("login")
	d := w.startDaemon()

	eventually(t, "both activities in /status", func() bool { return d.status().Counts["delivered"] == 2 })

	s := d.status()
	if !s.Healthy || s.Token != "valid" || s.LastCycle == nil || s.LastCycle.Error != "" || s.LastCycle.FinishedAt.Before(s.LastCycle.StartedAt) {
		t.Fatalf("status = %+v", s)
	}
}

func TestRevokedTokenMakesRunUnhealthyAndLoginRestoresItWithoutRestart(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	d := w.startDaemon()
	eventually(t, "a healthy first cycle", func() bool { return d.status().Token == "valid" })

	w.fake.RevokeTokens()

	eventually(t, "/healthz to report 503", func() bool { return d.healthz() == http.StatusServiceUnavailable })
	assertContains(t, d.errOut(), "revoked", "token age", "login")
	if s := d.status(); s.Token != "rejected" || s.Healthy {
		t.Fatalf("status after revocation = %+v", s)
	}
	rejectedAt := d.status().LastCycle.StartedAt
	eventually(t, "another cycle with the rejected token", func() bool { return d.status().LastCycle.StartedAt.After(rejectedAt) })
	if !d.alive() {
		t.Fatalf("daemon exited after a revoked token\nstderr: %s", d.errOut())
	}

	w.addActivity("900000000000000002", 0)
	w.mustRun("login")

	eventually(t, "/healthz to recover", func() bool { return d.healthz() == http.StatusOK })
	eventually(t, "delivery after the new login", w.delivered("900000000000000002"))
}

func TestRunBeforeLoginWaitsUnhealthyForALogin(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	d := w.startDaemon()

	eventually(t, "/healthz to report 503", func() bool { return d.healthz() == http.StatusServiceUnavailable })
	if s := d.status(); s.Token != "missing" {
		t.Fatalf("token state before login = %q, want missing", s.Token)
	}

	w.mustRun("login")

	eventually(t, "delivery after login", w.delivered("900000000000000002"))
	eventually(t, "/healthz to recover", func() bool { return d.healthz() == http.StatusOK })
}

func TestRunStopsOnACorruptLedger(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	corrupt := []byte(`{"version":1,"since":"2026-10-08","activities":{"9`)
	if err := os.WriteFile(w.ledgerPath(), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	w.env["POLL_INTERVAL_TESTING"] = "200ms"
	w.env["HTTP_ADDR"] = "127.0.0.1:0"

	r := w.run("run")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "ledger")
	if after, err := os.ReadFile(w.ledgerPath()); err != nil || string(after) != string(corrupt) {
		t.Fatalf("ledger was modified: %q, %v", after, err)
	}
}

func TestPollIntervalOutsideTheAllowedRangeIsRejectedAtStartup(t *testing.T) {
	for _, value := range []string{"899", "60", "-1", "hourly", "604801", "9300000000"} {
		t.Run(value, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.mustRun("login")
			w.env["POLL_INTERVAL"] = value
			w.env["HTTP_ADDR"] = "127.0.0.1:0"

			r := w.run("run")

			if r.code == 0 {
				t.Fatal("expected a non-zero exit")
			}
			assertContains(t, r.stderr, "POLL_INTERVAL", "900", "604800")
		})
	}
}

func TestPollIntervalAtTheMinimumIsAccepted(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["POLL_INTERVAL"] = "900"
	w.env["POLL_INTERVAL_TESTING"] = ""

	d := w.startDaemon()

	eventually(t, "a first cycle", func() bool { return d.status().LastCycle != nil })
}

func TestHealthcheckExitCodeMatchesHealthz(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	d := w.startDaemon()
	eventually(t, "a healthy first cycle", func() bool { return d.status().Token == "valid" })

	if r := w.run("healthcheck"); r.code != 0 {
		t.Fatalf("healthcheck exited %d while /healthz is 200\nstderr: %s", r.code, r.stderr)
	}

	w.fake.RevokeTokens()
	eventually(t, "/healthz to report 503", func() bool { return d.healthz() == http.StatusServiceUnavailable })

	if r := w.run("healthcheck"); r.code == 0 {
		t.Fatal("healthcheck exited 0 while /healthz is 503")
	}
}

func TestHealthcheckFailsWhenNothingIsListening(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["HTTP_ADDR"] = "127.0.0.1:1"

	if r := w.run("healthcheck"); r.code == 0 {
		t.Fatal("healthcheck exited 0 with no daemon running")
	}
}

func TestRunStopsCleanlyOnSIGTERM(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	d := w.startDaemon()
	eventually(t, "a first cycle", func() bool { return d.status().LastCycle != nil })

	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}

	select {
	case <-d.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not stop on SIGTERM")
	}
	if d.code != 0 {
		t.Fatalf("exit code after SIGTERM = %d, want 0\nstderr: %s", d.code, d.errOut())
	}
}

func TestStatusCountsEveryLedgerStatus(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	d := w.startDaemon()

	eventually(t, "a first cycle", func() bool { return d.status().LastCycle != nil })

	counts := d.status().Counts
	for _, status := range []string{"delivered", "no-file"} {
		if n, ok := counts[status]; !ok || n != 0 {
			t.Errorf("counts[%q] = %d, %v; want 0, present", status, n, ok)
		}
	}
}

func TestRunWithACorruptTokenFileIsUnhealthyUntilLogin(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	if err := os.WriteFile(w.tokens+"/token.json", []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := w.startDaemon()

	eventually(t, "/healthz to report 503", func() bool { return d.healthz() == http.StatusServiceUnavailable })
	if s := d.status(); s.Token != "unreadable" {
		t.Fatalf("token state = %q, want unreadable", s.Token)
	}

	w.mustRun("login")

	eventually(t, "delivery after login", w.delivered("900000000000000002"))
}

func TestRunStopsWhenTheTokenFileCannotBeRead(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	if err := os.Chmod(w.tokens+"/token.json", 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(w.tokens+"/token.json", 0o600) })
	w.env["POLL_INTERVAL_TESTING"] = "200ms"
	w.env["HTTP_ADDR"] = "127.0.0.1:0"

	r := w.run("run")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "token")
}

func TestRunStopsOnAnInvalidSinceForANewLedger(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.mustRun("login")
	w.env["SINCE"] = "last month"
	w.env["POLL_INTERVAL_TESTING"] = "200ms"
	w.env["HTTP_ADDR"] = "127.0.0.1:0"

	r := w.run("run")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "SINCE")
}

func TestRunStaysHealthyAndSendsNothingWhileBackingOffAfterA429(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity("900000000000000002", 1)
	w.mustRun("login")
	w.fake.InjectFaults(fakecoros.Query, fakecoros.Throttle)
	d := w.startDaemon()

	eventually(t, "the throttled cycle in /status", func() bool {
		s := d.status()
		return s.LastCycle != nil && strings.Contains(s.LastCycle.Error, "429")
	})
	throttledAt := d.status().LastCycle.StartedAt
	sent := len(w.fake.Requests())
	eventually(t, "a cycle while backing off", func() bool { return d.status().LastCycle.StartedAt.After(throttledAt) })

	if s := d.status(); !s.Healthy || !strings.Contains(s.LastCycle.Error, "backing off until") {
		t.Fatalf("status while backing off = %+v", s)
	}
	if d.healthz() != http.StatusOK {
		t.Fatal("/healthz is not 200 while backing off")
	}
	if n := len(w.fake.Requests()); n != sent {
		t.Fatalf("%d requests reached COROS while backing off", n-sent)
	}
}
