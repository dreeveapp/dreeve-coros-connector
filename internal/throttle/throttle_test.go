package throttle_test

import (
	"context"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
	"github.com/xballoy/dreeve-coros-connector/internal/throttle"
)

type fakeClock struct {
	now   time.Time
	waits []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 7, 8, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.waits = append(c.waits, d)
	c.now = c.now.Add(d)
	ch := make(chan time.Time, 1)
	ch <- c.now
	return ch
}

func newThrottle(clock *fakeClock, backoff *ledger.Backoff) *throttle.Throttle {
	return throttle.New(throttle.Config{
		Clock:       clock,
		Delay:       2 * time.Second,
		BaseBackoff: 15 * time.Minute,
		MaxBackoff:  6 * time.Hour,
	}, backoff)
}

func pace(t *testing.T, th *throttle.Throttle) {
	t.Helper()
	if err := th.Pace(context.Background()); err != nil {
		t.Fatalf("Pace: %v", err)
	}
}

func download(t *testing.T, th *throttle.Throttle, clock *fakeClock, took time.Duration) (time.Time, time.Time) {
	t.Helper()
	pace(t, th)
	started := clock.Now()
	clock.now = clock.now.Add(took)
	th.Downloaded()
	return started, clock.Now()
}

func TestFirstDownloadIsNotDelayed(t *testing.T) {
	clock := newFakeClock()
	th := newThrottle(clock, nil)

	pace(t, th)

	if len(clock.waits) != 0 {
		t.Fatalf("first download waited %v", clock.waits)
	}
}

func TestEachDownloadStartsAtLeastTheDelayAfterThePreviousOneEnded(t *testing.T) {
	clock := newFakeClock()
	th := newThrottle(clock, nil)

	_, firstEnded := download(t, th, clock, 1500*time.Millisecond)
	secondStarted, secondEnded := download(t, th, clock, 300*time.Millisecond)
	thirdStarted, _ := download(t, th, clock, 0)

	if gap := secondStarted.Sub(firstEnded); gap != 2*time.Second {
		t.Errorf("second download started %v after the first ended, want 2s", gap)
	}
	if gap := thirdStarted.Sub(secondEnded); gap != 2*time.Second {
		t.Errorf("third download started %v after the second ended, want 2s", gap)
	}
}

func TestDownloadAfterTheDelayHasElapsedIsNotDelayed(t *testing.T) {
	clock := newFakeClock()
	th := newThrottle(clock, nil)
	download(t, th, clock, time.Second)
	clock.now = clock.now.Add(5 * time.Second)

	pace(t, th)

	if len(clock.waits) != 0 {
		t.Fatalf("download waited %v although the delay had elapsed", clock.waits)
	}
}

func TestPaceStopsWhenTheContextIsCancelled(t *testing.T) {
	clock := newFakeClock()
	th := throttle.New(throttle.Config{Clock: clock, Delay: time.Hour, BaseBackoff: time.Minute, MaxBackoff: time.Hour}, nil)
	download(t, th, clock, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := th.Pace(ctx); err == nil {
		t.Fatal("expected Pace to return the context error")
	}
}

func TestBackoffDoublesAfterEachConsecutive429AndNeverExceedsTheCap(t *testing.T) {
	clock := newFakeClock()
	th := throttle.New(throttle.Config{
		Clock:       clock,
		Delay:       2 * time.Second,
		BaseBackoff: 15 * time.Minute,
		MaxBackoff:  100 * time.Minute,
	}, nil)

	var got []time.Duration
	for range 5 {
		until := th.Throttled()
		got = append(got, until.Sub(clock.Now()))
		clock.now = until
	}

	want := []time.Duration{15 * time.Minute, 30 * time.Minute, 60 * time.Minute, 100 * time.Minute, 100 * time.Minute}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("backoff after 429 #%d = %v, want %v", i+1, got[i], want[i])
		}
	}
}

func TestBackingOffUntilTheBackoffElapses(t *testing.T) {
	clock := newFakeClock()
	th := newThrottle(clock, nil)
	if _, ok := th.BackingOff(); ok {
		t.Fatal("backing off before any 429")
	}
	until := th.Throttled()

	clock.now = until.Add(-time.Second)
	if got, ok := th.BackingOff(); !ok || !got.Equal(until) {
		t.Fatalf("BackingOff() = %v, %v one second before the backoff elapses, want %v, true", got, ok, until)
	}
	clock.now = until
	if _, ok := th.BackingOff(); ok {
		t.Fatal("still backing off once the backoff elapsed")
	}
}

func TestBackoffIsRestoredFromTheLedger(t *testing.T) {
	clock := newFakeClock()
	stored := &ledger.Backoff{Until: clock.Now().Add(10 * time.Minute), Count: 2}
	th := newThrottle(clock, stored)

	if got, ok := th.BackingOff(); !ok || !got.Equal(stored.Until) {
		t.Fatalf("BackingOff() = %v, %v, want the stored backoff", got, ok)
	}
	clock.now = stored.Until
	if until := th.Throttled(); until.Sub(clock.Now()) != time.Hour {
		t.Fatalf("third consecutive 429 backs off %v, want 1h", until.Sub(clock.Now()))
	}
	if b := th.Backoff(); b == nil || b.Count != 3 || !b.Until.Equal(clock.Now().Add(time.Hour)) {
		t.Fatalf("Backoff() = %+v, want count 3 until one hour from now", b)
	}
}

func TestRecoveringResetsTheBackoff(t *testing.T) {
	clock := newFakeClock()
	th := newThrottle(clock, &ledger.Backoff{Until: clock.Now(), Count: 4})

	th.Recovered()

	if b := th.Backoff(); b != nil {
		t.Fatalf("Backoff() = %+v after recovering, want none", b)
	}
	if until := th.Throttled(); until.Sub(clock.Now()) != 15*time.Minute {
		t.Fatalf("first 429 after recovering backs off %v, want 15m", until.Sub(clock.Now()))
	}
}
