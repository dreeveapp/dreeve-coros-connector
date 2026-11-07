package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func (w *world) withoutCredentials() {
	delete(w.env, "COROS_EMAIL")
	delete(w.env, "COROS_PASSWORD")
}

func (w *world) secretFile(name, content string) string {
	w.t.Helper()
	path := filepath.Join(w.t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		w.t.Fatal(err)
	}
	return path
}

func (w *world) savedHost() string {
	w.t.Helper()
	data, err := os.ReadFile(filepath.Join(w.tokens, "token.json"))
	if err != nil {
		w.t.Fatal(err)
	}
	var tok struct {
		Host string `json:"host"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		w.t.Fatalf("token file %s: %v", data, err)
	}
	return tok.Host
}

func (w *world) loginRegions() []int {
	var regions []int
	for _, r := range w.requestsTo(fakecoros.Login) {
		regions = append(regions, r.Region)
	}
	return regions
}

func (w *world) assertNothingSaved() {
	w.t.Helper()
	if files := w.tokenFiles(); len(files) != 0 {
		w.t.Fatalf("token persisted: %v", files)
	}
}

func TestInteractiveLoginPromptsForEmailAndHiddenPassword(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.withoutCredentials()

	term := w.startInTerminal("login")
	term.expect("Email: ")
	term.send(email + "\r")
	term.expect("Password: ")
	term.send(password + "\r")

	if code := term.exitCode(); code != 0 {
		t.Fatalf("login exited %d, terminal:\n%s", code, term.screen())
	}
	if strings.Contains(term.screen(), password) {
		t.Fatalf("password echoed to the terminal:\n%s", term.screen())
	}
	assertContains(t, term.screen(), email)
	w.savedToken()
}

func TestInteractiveLoginPromptsOnlyForWhatIsMissing(t *testing.T) {
	w := newWorld(t, 1, 1)
	delete(w.env, "COROS_PASSWORD")

	term := w.startInTerminal("login")
	term.expect("Password: ")
	term.send(password + "\r")

	if code := term.exitCode(); code != 0 {
		t.Fatalf("login exited %d, terminal:\n%s", code, term.screen())
	}
	if strings.Contains(term.screen(), "Email: ") {
		t.Fatalf("prompted for an email that COROS_EMAIL already gives:\n%s", term.screen())
	}
	w.savedToken()
}

func TestInterruptingThePasswordPromptRestoresEchoAndSavesNothing(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.withoutCredentials()

	term := w.startInTerminal("login")
	term.expect("Email: ")
	term.send(email + "\r")
	term.expect("Password: ")
	term.interrupt()

	if code := term.exitCode(); code == 0 {
		t.Fatalf("interrupted login exited 0, terminal:\n%s", term.screen())
	}
	term.send("echo-is-back")
	term.expect("echo-is-back")
	w.assertNothingSaved()
}

func TestNonInteractiveLoginWithoutCredentialsListsTheAcceptedVariables(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.withoutCredentials()

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "COROS_EMAIL", "COROS_EMAIL_FILE", "COROS_PASSWORD", "COROS_PASSWORD_FILE")
	if regions := w.loginRegions(); len(regions) != 0 {
		t.Fatalf("logged in without credentials: %v", regions)
	}
}

func TestLoginReadsCredentialsFromSecretFiles(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.withoutCredentials()
	w.env["COROS_EMAIL_FILE"] = w.secretFile("email", email+"\n")
	w.env["COROS_PASSWORD_FILE"] = w.secretFile("password", password+"\n")

	r := w.mustRun("login")

	w.savedToken()
	if strings.Contains(r.stdout+r.stderr, password) {
		t.Fatalf("password in output:\n%s%s", r.stdout, r.stderr)
	}
}

func TestLoginRejectsAVariableSetTogetherWithItsFile(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["COROS_PASSWORD_FILE"] = w.secretFile("password", password)

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "COROS_PASSWORD", "COROS_PASSWORD_FILE")
	w.assertNothingSaved()
}

func TestLoginWithAMissingSecretFileNamesIt(t *testing.T) {
	w := newWorld(t, 1, 1)
	missing := filepath.Join(t.TempDir(), "no-such-secret")
	delete(w.env, "COROS_PASSWORD")
	w.env["COROS_PASSWORD_FILE"] = missing

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "COROS_PASSWORD_FILE", missing)
	w.assertNothingSaved()
}

func TestLoginWithAnUnknownRegionAsksForCorosRegionAndSavesNothing(t *testing.T) {
	w := newWorld(t, 4, 3)

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "region 4", "COROS_REGION")
	w.assertNothingSaved()
}

func TestCorosRegionChoosesTheHostAndIgnoresTheReportedRegion(t *testing.T) {
	for name, id := range map[string]int{"us": 1, "cn": 2, "eu": 3} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, 9, id)
			w.env["COROS_REGION"] = name

			w.mustRun("login")

			if host := w.savedHost(); host != w.fake.RegionURL(id) {
				t.Errorf("saved host %q, want %q", host, w.fake.RegionURL(id))
			}
			if regions := w.loginRegions(); len(regions) != 1 || regions[0] != id {
				t.Errorf("logged in on regions %v, want [%d]", regions, id)
			}
		})
	}
}

func TestCorosRegionThatIsNotTheAccountsRegionFailsAndSavesNothing(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["COROS_REGION"] = "eu"

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "COROS_REGION", "rejected")
	w.assertNothingSaved()
}

func TestInvalidCorosRegionFailsBeforeLoggingIn(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["COROS_REGION"] = "asia"

	r := w.run("login")

	if r.code == 0 {
		t.Fatal("expected a non-zero exit")
	}
	assertContains(t, r.stderr, "COROS_REGION", "asia", "us", "eu", "cn")
	if regions := w.loginRegions(); len(regions) != 0 {
		t.Fatalf("logged in despite an invalid COROS_REGION: %v", regions)
	}
}
