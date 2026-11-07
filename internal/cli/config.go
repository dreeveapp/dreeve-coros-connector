package cli

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
)

type paths struct {
	watch  string
	state  string
	tokens string
}

func loadPaths(getenv Env) paths {
	return paths{
		watch:  envOr(getenv, "WATCH_DIR", "/watch"),
		state:  envOr(getenv, "STATE_DIR", "/state"),
		tokens: envOr(getenv, "TOKENS_DIR", "/tokens"),
	}
}

func (p paths) ledgerFile() string { return filepath.Join(p.state, "ledger.json") }

func newClient(getenv Env) (*coros.Client, error) {
	hosts := coros.DefaultHosts()
	if override := getenv("COROS_API_HOSTS"); override != "" {
		parsed, err := coros.ParseHosts(override)
		if err != nil {
			return nil, fmt.Errorf("COROS_API_HOSTS: %w", err)
		}
		hosts = parsed
	}
	client := coros.NewClient(hosts)
	timeout, err := positiveDuration(getenv, "HTTP_TIMEOUT_TESTING")
	if err != nil {
		return nil, err
	}
	if timeout > 0 {
		client.SetTimeout(timeout)
	}
	return client, nil
}

const (
	baseBackoff       = 15 * time.Minute
	maxBackoffSeconds = 7 * 24 * 3600
)

type limits struct {
	maxDownloads int
	maxAttempts  int
	delay        time.Duration
	maxBackoff   time.Duration
}

func loadLimits(getenv Env) (limits, error) {
	var l limits
	var err error
	if l.maxDownloads, err = positiveInt(getenv, "MAX_DOWNLOADS_PER_CYCLE", 25); err != nil {
		return limits{}, err
	}
	if l.maxAttempts, err = positiveInt(getenv, "MAX_ATTEMPTS", 5); err != nil {
		return limits{}, err
	}
	backoff, err := positiveInt(getenv, "MAX_BACKOFF_SECONDS", 21600)
	if err != nil {
		return limits{}, err
	}
	if backoff > maxBackoffSeconds {
		return limits{}, fmt.Errorf("MAX_BACKOFF_SECONDS must be at most %d (one week), got %d", maxBackoffSeconds, backoff)
	}
	l.maxBackoff = time.Duration(backoff) * time.Second
	if l.delay, err = positiveDuration(getenv, "DOWNLOAD_DELAY_TESTING"); err != nil || l.delay > 0 {
		return l, err
	}
	delay, err := positiveInt(getenv, "DOWNLOAD_DELAY_SECONDS", 2)
	if err != nil {
		return limits{}, err
	}
	l.delay = time.Duration(delay) * time.Second
	return l, nil
}

func positiveInt(getenv Env, key string, fallback int) (int, error) {
	raw := getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s must be a whole number of at least 1, got %q", key, raw)
	}
	return n, nil
}

func envOr(getenv Env, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func lookbackDays(getenv Env) (int, error) {
	raw := envOr(getenv, "LOOKBACK_DAYS", "7")
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("LOOKBACK_DAYS must be a non-negative number of days, got %q", raw)
	}
	return n, nil
}

func resolveSince(raw string, today time.Time) (time.Time, error) {
	if days, ok := strings.CutPrefix(raw, "-"); ok {
		if n, found := strings.CutSuffix(days, "d"); found {
			if d, err := strconv.Atoi(n); err == nil && d >= 0 {
				return today.AddDate(0, 0, -d), nil
			}
		}
	}
	t, err := time.ParseInLocation(time.DateOnly, raw, today.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("SINCE must be -<days>d or YYYY-MM-DD, got %q", raw)
	}
	return t, nil
}

func timeZone(getenv Env) (*time.Location, error) {
	name := getenv("TZ")
	if name == "" {
		return time.UTC, nil
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("TZ %q is not a known time zone: %w", name, err)
	}
	return zone, nil
}

func positiveDuration(getenv Env, key string) (time.Duration, error) {
	raw := getenv(key)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration, got %q", key, raw)
	}
	return d, nil
}
