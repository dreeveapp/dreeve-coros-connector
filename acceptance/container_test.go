package acceptance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

const (
	smokeImage = "dreeve-coros-connector:smoke"
	smokeUser  = "4242:4242"
)

func TestContainerSmoke(t *testing.T) {
	if os.Getenv("CONTAINER_SMOKE") != "1" {
		t.Skip("set CONTAINER_SMOKE=1 to build and run the image")
	}
	docker(t, "build", "-t", smokeImage, "..")

	if user := strings.TrimSpace(docker(t, "image", "inspect", "--format", "{{.Config.User}}", smokeImage)); user == "" || user == "root" || user == "0" {
		t.Fatalf("image runs as %q, want a non-root default user", user)
	}

	fake := fakecoros.New(t, fakecoros.Config{
		FixturesDir:    "../testdata/coros",
		Email:          email,
		Password:       password,
		RegionID:       1,
		ListenAddr:     "0.0.0.0:0",
		AdvertisedHost: "host.docker.internal",
	})
	fake.AddActivity(fakecoros.Activity{LabelID: "900000000000000002", SportType: 100, Name: "Run", Start: nowMinusDays(1), Content: []byte("FIT smoke")})

	volumes := map[string]string{"/watch": t.TempDir(), "/state": t.TempDir(), "/tokens": t.TempDir()}
	for _, dir := range volumes {
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	runArgs := func(command string, extra ...string) []string {
		args := []string{"run", "--rm", "--read-only", "--user", smokeUser, "--add-host", "host.docker.internal:host-gateway",
			"-e", "COROS_API_HOSTS=" + fake.HostsEnv(), "-e", "COROS_EMAIL=" + email, "-e", "COROS_PASSWORD=" + password}
		for target, source := range volumes {
			args = append(args, "-v", source+":"+target)
		}
		args = append(args, extra...)
		return append(args, smokeImage, command)
	}
	docker(t, runArgs("login")...)
	docker(t, runArgs("sync-once")...)

	delivered := filepath.Join(volumes["/watch"], "coros_900000000000000002.fit")
	assertMode(t, delivered, 0o644)
	assertMode(t, filepath.Join(volumes["/tokens"], "token.json"), 0o600)
	if data, err := os.ReadFile(delivered); err != nil || string(data) != "FIT smoke" {
		t.Fatalf("delivered file content %q, err %v", data, err)
	}
	if runtime.GOOS == "linux" {
		info, err := os.Stat(delivered)
		if err != nil {
			t.Fatal(err)
		}
		if uid := info.Sys().(*syscall.Stat_t).Uid; uid != 4242 {
			t.Fatalf("delivered file owned by uid %d, want 4242", uid)
		}
	}

	container := strings.TrimSpace(docker(t, runArgs("run", "--detach", "-e", "POLL_INTERVAL_TESTING=1s",
		"--health-interval=1s", "--health-timeout=5s", "--health-retries=1", "--health-start-period=0s")...))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", container).Run() })
	awaitHealth(t, container, "healthy")

	fake.RevokeTokens()
	awaitHealth(t, container, "unhealthy")

	docker(t, runArgs("login")...)
	awaitHealth(t, container, "healthy")
}

func awaitHealth(t *testing.T, container, want string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		got = strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.Health.Status}}", container))
		if got == want {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("container health = %q, want %q\nlogs: %s", got, want, docker(t, "logs", container))
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode().Perm(), want)
	}
}
