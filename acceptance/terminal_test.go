package acceptance_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

type terminal struct {
	t      *testing.T
	ptmx   *os.File
	cmd    *exec.Cmd
	mu     sync.Mutex
	output strings.Builder
	exited chan int
}

func (w *world) startInTerminal(args ...string) *terminal {
	w.t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		w.t.Fatalf("open pty: %v", err)
	}
	w.t.Cleanup(func() {
		_ = ptmx.Close()
		_ = tty.Close()
	})
	cmd := w.command(args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	if err := cmd.Start(); err != nil {
		w.t.Fatalf("start %v: %v", args, err)
	}
	w.t.Cleanup(func() { _ = cmd.Process.Kill() })
	term := &terminal{t: w.t, ptmx: ptmx, cmd: cmd, exited: make(chan int, 1)}
	go term.capture()
	go func() {
		err := cmd.Wait()
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			term.exited <- exitErr.ExitCode()
		case err != nil:
			term.exited <- -1
		default:
			term.exited <- 0
		}
	}()
	return term
}

func (term *terminal) capture() {
	buf := make([]byte, 1024)
	for {
		n, err := term.ptmx.Read(buf)
		term.mu.Lock()
		term.output.Write(buf[:n])
		term.mu.Unlock()
		if err != nil {
			return
		}
	}
}

func (term *terminal) screen() string {
	term.mu.Lock()
	defer term.mu.Unlock()
	return term.output.String()
}

func (term *terminal) expect(text string) {
	term.t.Helper()
	eventually(term.t, fmt.Sprintf("terminal to show %q", text), func() bool { return strings.Contains(term.screen(), text) })
}

func (term *terminal) send(keys string) {
	term.t.Helper()
	if _, err := term.ptmx.WriteString(keys); err != nil {
		term.t.Fatalf("type %q: %v", keys, err)
	}
}

func (term *terminal) interrupt() {
	term.t.Helper()
	if err := term.cmd.Process.Signal(os.Interrupt); err != nil {
		term.t.Fatalf("interrupt: %v", err)
	}
}

func (term *terminal) exitCode() int {
	term.t.Helper()
	select {
	case code := <-term.exited:
		return code
	case <-time.After(30 * time.Second):
		term.t.Fatalf("process did not exit within 30s, terminal:\n%s", term.screen())
		return -1
	}
}
