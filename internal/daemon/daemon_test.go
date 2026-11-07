package daemon_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/daemon"
)

type wait struct {
	d    time.Duration
	fire chan time.Time
}

type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan wait
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC), waits: make(chan wait)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	c.waits <- wait{d: d, fire: ch}
	return ch
}

func (c *fakeClock) add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

func (c *fakeClock) elapse(w wait) {
	w.fire <- c.add(w.d)
}

func (c *fakeClock) nextWait(t *testing.T) wait {
	t.Helper()
	select {
	case w := <-c.waits:
		return w
	case <-time.After(5 * time.Second):
		t.Fatal("the daemon never waited for the next cycle")
		return wait{}
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

type harness struct {
	clock   *fakeClock
	starts  chan time.Time
	d       *daemon.Daemon
	err     error
	stopped chan struct{}
	cancel  context.CancelFunc
}

func start(t *testing.T, interval time.Duration, cycle func(*fakeClock) (daemon.Report, error)) *harness {
	t.Helper()
	h := &harness{clock: newFakeClock(), starts: make(chan time.Time, 1), stopped: make(chan struct{})}
	h.d = daemon.New(daemon.Config{
		Interval: interval,
		Clock:    h.clock,
		Cycle: func(context.Context) (daemon.Report, error) {
			h.starts <- h.clock.Now()
			return cycle(h.clock)
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	go func() {
		h.err = h.d.Run(ctx)
		close(h.stopped)
	}()
	t.Cleanup(func() {
		cancel()
		for {
			select {
			case <-h.stopped:
				return
			case <-h.clock.waits:
			case <-h.starts:
			case <-time.After(5 * time.Second):
				t.Error("daemon did not stop")
				return
			}
		}
	})
	return h
}

func (h *harness) result(t *testing.T) error {
	t.Helper()
	receive(t, h.stopped)
	return h.err
}

func validToken(*fakeClock) (daemon.Report, error) {
	return daemon.Report{Token: daemon.TokenValid}, nil
}

func TestFirstCycleRunsAtStartup(t *testing.T) {
	h := start(t, time.Hour, validToken)

	if got := receive(t, h.starts); !got.Equal(newFakeClock().now) {
		t.Fatalf("first cycle started at %v, want at startup", got)
	}
}

func TestCycleStartsStayWithinTheIntervalPlusOrMinusTenPercent(t *testing.T) {
	const interval = time.Hour
	h := start(t, interval, validToken)

	previous := receive(t, h.starts)
	gaps := map[time.Duration]bool{}
	for range 200 {
		h.clock.elapse(h.clock.nextWait(t))
		next := receive(t, h.starts)
		gap := next.Sub(previous)
		if gap < 54*time.Minute || gap > 66*time.Minute {
			t.Fatalf("cycle started %v after the previous one, want within 54m-66m", gap)
		}
		gaps[gap] = true
		previous = next
	}
	if len(gaps) < 10 {
		t.Fatalf("only %d distinct gaps over 200 cycles, want jitter", len(gaps))
	}
}

func TestOverdueCycleStartsAsSoonAsThePreviousOneEnds(t *testing.T) {
	h := start(t, time.Hour, func(c *fakeClock) (daemon.Report, error) {
		c.add(2 * time.Hour)
		return daemon.Report{Token: daemon.TokenValid}, nil
	})

	first := receive(t, h.starts)
	second := receive(t, h.starts)

	if gap := second.Sub(first); gap != 2*time.Hour {
		t.Fatalf("second cycle started %v after the first, want right after the first ended", gap)
	}
}

func TestFailingCycleStopsTheDaemonWithItsError(t *testing.T) {
	broken := errors.New("ledger is corrupt")
	h := start(t, time.Hour, func(*fakeClock) (daemon.Report, error) { return daemon.Report{}, broken })

	receive(t, h.starts)

	if err := h.result(t); !errors.Is(err, broken) {
		t.Fatalf("Run returned %v, want %v", err, broken)
	}
}

func TestCancellingStopsTheDaemonCleanly(t *testing.T) {
	h := start(t, time.Hour, validToken)
	receive(t, h.starts)
	h.clock.nextWait(t)

	h.cancel()

	if err := h.result(t); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}

func scripted(reports ...daemon.Report) func(*fakeClock) (daemon.Report, error) {
	var mu sync.Mutex
	return func(c *fakeClock) (daemon.Report, error) {
		mu.Lock()
		defer mu.Unlock()
		c.add(time.Minute)
		r := reports[0]
		if len(reports) > 1 {
			reports = reports[1:]
		}
		return r, nil
	}
}

func get(t *testing.T, d *daemon.Daemon, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	d.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func status(t *testing.T, d *daemon.Daemon) daemon.Status {
	t.Helper()
	rec := get(t, d, "/status")
	if rec.Code != http.StatusOK {
		t.Fatalf("/status returned %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("/status content type %q", ct)
	}
	var s daemon.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("/status is not JSON: %v\n%s", err, rec.Body)
	}
	return s
}

func (h *harness) completeCycle(t *testing.T) wait {
	t.Helper()
	receive(t, h.starts)
	return h.clock.nextWait(t)
}

func TestHealthyBeforeTheFirstCycle(t *testing.T) {
	d := daemon.New(daemon.Config{Interval: time.Hour, Clock: newFakeClock()})

	if code := get(t, d, "/healthz").Code; code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", code)
	}
	if s := status(t, d); s.Token != daemon.TokenUnknown || !s.Healthy || s.LastCycle != nil {
		t.Fatalf("status before the first cycle = %+v", s)
	}
}

func TestRejectedTokenIsUnhealthyUntilACycleSucceeds(t *testing.T) {
	h := start(t, time.Hour, scripted(
		daemon.Report{Token: daemon.TokenRejected, Err: errors.New("COROS rejected the saved token")},
		daemon.Report{Token: daemon.TokenValid},
	))

	next := h.completeCycle(t)
	if code := get(t, h.d, "/healthz").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz after a rejected token = %d, want 503", code)
	}
	if s := status(t, h.d); s.Healthy || s.Token != daemon.TokenRejected || s.LastCycle.Error != "COROS rejected the saved token" {
		t.Fatalf("status after a rejected token = %+v", s)
	}

	h.clock.elapse(next)
	h.completeCycle(t)
	if code := get(t, h.d, "/healthz").Code; code != http.StatusOK {
		t.Fatalf("/healthz after recovery = %d, want 200", code)
	}
}

func TestMissingTokenIsUnhealthy(t *testing.T) {
	h := start(t, time.Hour, scripted(daemon.Report{Token: daemon.TokenMissing, Err: errors.New("not logged in")}))

	h.completeCycle(t)

	if code := get(t, h.d, "/healthz").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz without a token = %d, want 503", code)
	}
}

func TestCycleThatCannotTellTheTokenStateKeepsThePreviousHealth(t *testing.T) {
	h := start(t, time.Hour, scripted(
		daemon.Report{Token: daemon.TokenRejected, Err: errors.New("rejected")},
		daemon.Report{Token: daemon.TokenUnchanged, Err: errors.New("connection refused")},
	))
	h.clock.elapse(h.completeCycle(t))

	h.completeCycle(t)

	s := status(t, h.d)
	if s.Healthy || s.Token != daemon.TokenRejected || s.LastCycle.Error != "connection refused" {
		t.Fatalf("status after a network error = %+v", s)
	}
}

func TestStatusReportsTheLastCycleAndLedgerCounts(t *testing.T) {
	h := start(t, time.Hour, scripted(daemon.Report{Token: daemon.TokenValid, Counts: map[string]int{"delivered": 3, "no-file": 1}}))
	begin := h.clock.Now()

	h.completeCycle(t)

	s := status(t, h.d)
	if !s.Healthy || s.Token != daemon.TokenValid {
		t.Fatalf("status = %+v", s)
	}
	if s.LastCycle == nil || !s.LastCycle.StartedAt.Equal(begin) || !s.LastCycle.FinishedAt.Equal(begin.Add(time.Minute)) || s.LastCycle.Error != "" {
		t.Fatalf("last cycle = %+v", s.LastCycle)
	}
	if s.Counts["delivered"] != 3 || s.Counts["no-file"] != 1 {
		t.Fatalf("counts = %v", s.Counts)
	}
}

func TestStatusIsReadableWhileCyclesRun(t *testing.T) {
	h := start(t, time.Hour, scripted(
		daemon.Report{Token: daemon.TokenValid, Counts: map[string]int{"delivered": 1}},
		daemon.Report{Token: daemon.TokenRejected, Err: errors.New("rejected")},
	))
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					get(t, h.d, "/status")
					get(t, h.d, "/healthz")
				}
			}
		})
	}

	for range 50 {
		h.clock.elapse(h.completeCycle(t))
	}
	close(stop)
	readers.Wait()
}

func TestCycleInterruptedByShutdownLeavesTheStatusUntouched(t *testing.T) {
	shutdown := make(chan context.CancelFunc, 1)
	h := start(t, time.Hour, func(*fakeClock) (daemon.Report, error) {
		(<-shutdown)()
		return daemon.Report{Token: daemon.TokenUnchanged, Err: context.Canceled}, nil
	})
	shutdown <- h.cancel
	receive(t, h.starts)

	if err := h.result(t); err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
	if s := status(t, h.d); s.LastCycle != nil || s.Token != daemon.TokenUnknown {
		t.Fatalf("status after an interrupted cycle = %+v", s)
	}
}

func TestUnreadableTokenIsUnhealthy(t *testing.T) {
	h := start(t, time.Hour, scripted(daemon.Report{Token: daemon.TokenUnreadable, Err: errors.New("token file is unreadable")}))

	h.completeCycle(t)

	if code := get(t, h.d, "/healthz").Code; code != http.StatusServiceUnavailable {
		t.Fatalf("/healthz with an unreadable token = %d, want 503", code)
	}
}
