package acceptance_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func (w *world) savedToken() string {
	w.t.Helper()
	data, err := os.ReadFile(filepath.Join(w.tokens, "token.json"))
	if err != nil {
		w.t.Fatal(err)
	}
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil || tok.Token == "" {
		w.t.Fatalf("token file %s: %v", data, err)
	}
	return tok.Token
}

type logRecord struct {
	Time  *string `json:"time"`
	Level *string `json:"level"`
	Msg   *string `json:"msg"`
}

func jsonRecords(t *testing.T, what, output string) []logRecord {
	t.Helper()
	var records []logRecord
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var record logRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("%s: line is not a JSON object: %v\n%s", what, err, scanner.Text())
		}
		if record.Time == nil || record.Level == nil || record.Msg == nil {
			t.Errorf("%s: line lacks time, level or msg: %s", what, scanner.Text())
		}
		records = append(records, record)
	}
	return records
}

func TestJSONLogsAreOneObjectPerLineAndNeverContainSecrets(t *testing.T) {
	const wrongPassword = "wrong-pass-7731"
	w := newWorld(t, 1, 1)
	w.env["LOG_FORMAT"] = "json"
	w.env["LOG_LEVEL"] = "debug"
	w.env["MAX_ATTEMPTS"] = "1"
	w.addActivity(labelID(0), 3)
	w.addActivity(labelID(1), 2, withoutFile)
	w.addActivity(labelID(2), 1)

	outputs := map[string]result{"login": w.mustRun("login")}
	firstToken := w.savedToken()
	w.fake.InjectFaults(fakecoros.File, fakecoros.ServerError)
	outputs["sync-once with failures"] = w.mustRun("sync-once")
	d := w.startDaemon()
	eventually(t, "a cycle completes", func() bool { return d.status().LastCycle != nil })
	outputs["healthcheck"] = w.mustRun("healthcheck")
	w.fake.RevokeTokens()
	eventually(t, "the daemon reports the revoked token", func() bool { return d.healthz() == 503 })
	outputs["sync-once with a revoked token"] = w.run("sync-once")
	outputs["login again"] = w.mustRun("login")
	secondToken := w.savedToken()
	w.env["COROS_PASSWORD"] = wrongPassword
	outputs["login with a wrong password"] = w.run("login")
	outputs["run"] = result{stdout: d.out(), stderr: d.errOut()}

	lines := 0
	for what, r := range outputs {
		if r.stdout != "" {
			t.Errorf("%s wrote to stdout instead of the log on stderr:\n%s", what, r.stdout)
		}
		lines += len(jsonRecords(t, what, r.stderr))
		for _, secret := range []string{firstToken, secondToken, password, wrongPassword, coros.PasswordHash(password), coros.PasswordHash(wrongPassword)} {
			if strings.Contains(r.stdout+r.stderr, secret) {
				t.Errorf("%s logged a secret %q:\n%s", what, secret, r.stderr)
			}
		}
	}
	if lines < 10 {
		t.Fatalf("expected the scenario to log at least 10 lines, got %d", lines)
	}
}

func TestLogLevelHidesLessSevereRecords(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(labelID(0), 1, withContent(goldenFIT(t, "coros-outdoor-run.fit")))
	w.mustRun("login")
	w.env["LOG_LEVEL"] = "warn"

	r := w.mustRun("sync-once")

	if r.stderr != "" || r.stdout != "" {
		t.Fatalf("expected no output at warn level for a clean sync\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	w.fake.RevokeTokens()
	w.addActivity(labelID(1), 1)
	r = w.run("sync-once")
	assertContains(t, r.stderr, "level=ERROR", "revoked")
}

func TestInvalidLoggingSettingsFailAtStartup(t *testing.T) {
	for name, value := range map[string]string{"LOG_LEVEL": "verbose", "LOG_FORMAT": "logfmt"} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.env[name] = value

			r := w.run("sync-once")

			if r.code == 0 {
				t.Fatal("expected a non-zero exit")
			}
			assertContains(t, r.stderr, name, value)
		})
	}
}
