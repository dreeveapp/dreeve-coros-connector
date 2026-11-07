package throttle

import (
	"context"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Config struct {
	Clock       Clock
	Delay       time.Duration
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
}

type Throttle struct {
	cfg               Config
	backoff           *ledger.Backoff
	lastDownloadEnded time.Time
}

func New(cfg Config, backoff *ledger.Backoff) *Throttle {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	t := &Throttle{cfg: cfg}
	if backoff != nil {
		b := *backoff
		t.backoff = &b
	}
	return t
}

func (t *Throttle) Pace(ctx context.Context) error {
	if !t.lastDownloadEnded.IsZero() {
		if wait := t.lastDownloadEnded.Add(t.cfg.Delay).Sub(t.cfg.Clock.Now()); wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.cfg.Clock.After(wait):
			}
		}
	}
	return ctx.Err()
}

func (t *Throttle) Downloaded() {
	t.lastDownloadEnded = t.cfg.Clock.Now()
}

func (t *Throttle) BackingOff() (time.Time, bool) {
	if t.backoff == nil || !t.cfg.Clock.Now().Before(t.backoff.Until) {
		return time.Time{}, false
	}
	return t.backoff.Until, true
}

func (t *Throttle) Throttled() time.Time {
	count := 1
	if t.backoff != nil {
		count = t.backoff.Count + 1
	}
	wait := t.cfg.BaseBackoff
	for i := 1; i < count && wait < t.cfg.MaxBackoff; i++ {
		wait *= 2
	}
	wait = min(wait, t.cfg.MaxBackoff)
	t.backoff = &ledger.Backoff{Until: t.cfg.Clock.Now().Add(wait).UTC(), Count: count}
	return t.backoff.Until
}

func (t *Throttle) Recovered() {
	t.backoff = nil
}

func (t *Throttle) Backoff() *ledger.Backoff {
	if t.backoff == nil {
		return nil
	}
	b := *t.backoff
	return &b
}
