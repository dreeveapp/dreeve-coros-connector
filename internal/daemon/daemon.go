package daemon

import (
	"context"
	"encoding/json"
	"maps"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"
)

const jitter = 0.1

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type TokenState string

const (
	TokenUnchanged  TokenState = ""
	TokenUnknown    TokenState = "unknown"
	TokenValid      TokenState = "valid"
	TokenRejected   TokenState = "rejected"
	TokenMissing    TokenState = "missing"
	TokenUnreadable TokenState = "unreadable"
)

func (s TokenState) healthy() bool {
	return s != TokenRejected && s != TokenMissing && s != TokenUnreadable
}

type Report struct {
	Token  TokenState
	Counts map[string]int
	Err    error
}

type Config struct {
	Interval time.Duration
	Clock    Clock
	Cycle    func(context.Context) (Report, error)
}

type Cycle struct {
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Error      string    `json:"error,omitempty"`
}

type Status struct {
	Healthy   bool           `json:"healthy"`
	Token     TokenState     `json:"token"`
	LastCycle *Cycle         `json:"lastCycle,omitempty"`
	Counts    map[string]int `json:"counts"`
}

type Daemon struct {
	cfg Config

	mu     sync.Mutex
	status Status
}

func New(cfg Config) *Daemon {
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	return &Daemon{cfg: cfg, status: Status{Healthy: true, Token: TokenUnknown, Counts: map[string]int{}}}
}

func (d *Daemon) Run(ctx context.Context) error {
	for {
		started := d.cfg.Clock.Now()
		report, err := d.cfg.Cycle(ctx)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		d.record(started, report)
		next := started.Add(d.jittered())
		if wait := next.Sub(d.cfg.Clock.Now()); wait > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-d.cfg.Clock.After(wait):
			}
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}

func (d *Daemon) jittered() time.Duration {
	factor := 1 + jitter*(2*rand.Float64()-1)
	return time.Duration(float64(d.cfg.Interval) * factor)
}

func (d *Daemon) record(started time.Time, r Report) {
	cycle := &Cycle{StartedAt: started, FinishedAt: d.cfg.Clock.Now()}
	if r.Err != nil {
		cycle.Error = r.Err.Error()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status.LastCycle = cycle
	if r.Token != TokenUnchanged {
		d.status.Token = r.Token
		d.status.Healthy = r.Token.healthy()
	}
	if r.Counts != nil {
		d.status.Counts = maps.Clone(r.Counts)
	}
}

func (d *Daemon) snapshot() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	s := d.status
	s.Counts = maps.Clone(s.Counts)
	if s.LastCycle != nil {
		c := *s.LastCycle
		s.LastCycle = &c
	}
	return s
}

func (d *Daemon) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.snapshot().Healthy {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d.snapshot())
	})
	return mux
}
