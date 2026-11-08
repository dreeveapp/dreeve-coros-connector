package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/daemon"
)

const (
	minPollInterval = 900
	maxPollInterval = 7 * 24 * 3600
	defaultHTTPAddr = ":8080"
)

func pollInterval(getenv Env) (time.Duration, error) {
	if raw := getenv("POLL_INTERVAL_TESTING"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return 0, fmt.Errorf("POLL_INTERVAL_TESTING must be a positive duration, got %q", raw)
		}
		return d, nil
	}
	raw := envOr(getenv, "POLL_INTERVAL", "3600")
	n, err := strconv.Atoi(raw)
	if err != nil || n < minPollInterval || n > maxPollInterval {
		return 0, fmt.Errorf("POLL_INTERVAL must be a number of seconds between %d and %d, got %q", minPollInterval, maxPollInterval, raw)
	}
	return time.Duration(n) * time.Second, nil
}

func runDaemon(ctx context.Context, getenv Env, a *app) error {
	interval, err := pollInterval(getenv)
	if err != nil {
		return err
	}
	s, err := newSyncer(getenv, a)
	if err != nil {
		return err
	}
	if err := s.boot(); err != nil {
		return err
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", envOr(getenv, "HTTP_ADDR", defaultHTTPAddr))
	if err != nil {
		return fmt.Errorf("HTTP_ADDR: %w", err)
	}

	d := daemon.New(daemon.Config{
		Interval: interval,
		Cycle: func(ctx context.Context) (daemon.Report, error) {
			counts, err := s.cycle(ctx, "cycle")
			report, fatal := reportFor(counts, err)
			if report.Err != nil && ctx.Err() == nil {
				a.log.Error("cycle failed", "error", report.Err)
			}
			return report, fatal
		},
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	server := &http.Server{Handler: d.Handler(), ReadHeaderTimeout: 5 * time.Second, ErrorLog: slog.NewLogLogger(a.log.Handler(), slog.LevelError)}
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(listener)
		cancel()
	}()
	a.log.Info("serving /healthz and /status", "version", version, "addr", listener.Addr().String(), "interval", interval.String())

	runErr := d.Run(ctx)
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	_ = server.Shutdown(shutdownCtx)
	if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
		return fmt.Errorf("HTTP server: %w", serveErr)
	}
	return runErr
}

func healthcheck(ctx context.Context, getenv Env, a *app) error {
	addr := envOr(getenv, "HTTP_ADDR", defaultHTTPAddr)
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("HTTP_ADDR %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %s", url, resp.Status)
	}
	a.log.Info("healthy", "url", url)
	return nil
}
