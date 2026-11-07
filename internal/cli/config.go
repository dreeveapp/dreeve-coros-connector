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
	return coros.NewClient(hosts), nil
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
