package logging_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/xballoy/dreeve-coros-connector/internal/logging"
)

const (
	token    = "tok-3f9a1c"
	password = "correct horse"
)

func newLogger(t *testing.T, format logging.Format, level slog.Level) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var out bytes.Buffer
	secrets := &logging.Secrets{}
	secrets.Add(token)
	secrets.Add(password)
	secrets.Add("")
	return logging.New(&out, level, format, secrets), &out
}

func logEverywhere(log *slog.Logger) {
	log.Info("message carries " + token)
	log.Warn("attributes", "detail", "pwd="+password, "err", fmt.Errorf("wrapped: %w", errors.New("rejected "+token)))
	log.Error("group", slog.Group("request", slog.String("header", "accessToken: "+token)))
	log.With("session", token).WithGroup("cycle").Info("derived", "note", password)
	log.Info("named", "token", "not-a-registered-value", "password", "also-unregistered", "accessToken", "abc")
	log.Info("value", "any", struct{ Token string }{token})
	log.Info("bytes", "body", []byte(`{"pwd":"`+password+`"}`))
}

func TestNoRegisteredSecretOrSecretNamedAttributeReachesTheOutput(t *testing.T) {
	for _, format := range []logging.Format{logging.Text, logging.JSON} {
		t.Run(string(format), func(t *testing.T) {
			log, out := newLogger(t, format, slog.LevelDebug)

			logEverywhere(log)

			for _, secret := range []string{token, password, "not-a-registered-value", "also-unregistered", "abc"} {
				if strings.Contains(out.String(), secret) {
					t.Errorf("output contains %q:\n%s", secret, out.String())
				}
			}
			if got := strings.Count(out.String(), "[REDACTED]"); got < 11 {
				t.Errorf("expected every secret to be replaced by [REDACTED], found %d:\n%s", got, out.String())
			}
		})
	}
}

func TestJSONOutputIsOneObjectPerLine(t *testing.T) {
	log, out := newLogger(t, logging.JSON, slog.LevelDebug)

	logEverywhere(log)

	lines := 0
	scanner := bufio.NewScanner(out)
	for scanner.Scan() {
		lines++
		var record struct {
			Time  *string `json:"time"`
			Level *string `json:"level"`
			Msg   *string `json:"msg"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line %d is not a JSON object: %v\n%s", lines, err, scanner.Text())
		}
		if record.Time == nil || record.Level == nil || record.Msg == nil {
			t.Errorf("line %d lacks time, level or msg: %s", lines, scanner.Text())
		}
	}
	if lines != 7 {
		t.Fatalf("expected 7 lines, got %d", lines)
	}
}

func TestRecordsBelowTheLevelAreDropped(t *testing.T) {
	log, out := newLogger(t, logging.Text, slog.LevelWarn)

	log.Debug("debug line")
	log.Info("info line")
	log.Warn("warn line")

	if strings.Contains(out.String(), "debug line") || strings.Contains(out.String(), "info line") || !strings.Contains(out.String(), "warn line") {
		t.Fatalf("unexpected output at warn level:\n%s", out.String())
	}
}

func TestSecretsAddedLaterAreRedactedFromLoggersCreatedEarlier(t *testing.T) {
	var out bytes.Buffer
	secrets := &logging.Secrets{}
	log := logging.New(&out, slog.LevelInfo, logging.Text, secrets).With("component", "sync")

	secrets.Add(token)
	log.Info("loaded " + token)

	if strings.Contains(out.String(), token) {
		t.Fatalf("output contains the token:\n%s", out.String())
	}
}

func TestParseLevel(t *testing.T) {
	for raw, want := range map[string]slog.Level{"": slog.LevelInfo, "debug": slog.LevelDebug, "INFO": slog.LevelInfo, "warn": slog.LevelWarn, "Error": slog.LevelError} {
		got, err := logging.ParseLevel(raw)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := logging.ParseLevel("verbose"); err == nil {
		t.Error("ParseLevel(verbose) succeeded")
	}
}

func TestParseFormat(t *testing.T) {
	for raw, want := range map[string]logging.Format{"": logging.Text, "text": logging.Text, "JSON": logging.JSON} {
		got, err := logging.ParseFormat(raw)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := logging.ParseFormat("logfmt"); err == nil {
		t.Error("ParseFormat(logfmt) succeeded")
	}
}
