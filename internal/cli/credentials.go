package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

type credential struct {
	value  string
	source string
}

func credentialFromEnv(getenv Env, name string) (credential, error) {
	direct, file := getenv(name), getenv(name+"_FILE")
	switch {
	case direct != "" && file != "":
		return credential{}, fmt.Errorf("set either %s or %s_FILE, not both", name, name)
	case file != "":
		data, err := os.ReadFile(file)
		if err != nil {
			return credential{}, fmt.Errorf("read %s_FILE: %w", name, err)
		}
		value := strings.TrimRight(string(data), "\r\n")
		if value == "" {
			return credential{}, fmt.Errorf("%s_FILE %s is empty", name, file)
		}
		return credential{value: value, source: name + "_FILE"}, nil
	case direct != "":
		return credential{value: direct, source: name}, nil
	}
	return credential{}, nil
}

func missingCredentials(names ...string) error {
	missing := make([]string, 0, len(names))
	for _, name := range names {
		missing = append(missing, fmt.Sprintf("%s or %s_FILE", name, name))
	}
	return fmt.Errorf("no COROS credentials: set %s, or run login in a terminal to be prompted", strings.Join(missing, ", and "))
}

var errInterrupted = errors.New("login interrupted, nothing was saved")

type prompter struct {
	in  *os.File
	out io.Writer
}

func newPrompter(stdin io.Reader, out io.Writer) *prompter {
	f, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	return &prompter{in: f, out: out}
}

type answer struct {
	text string
	err  error
}

func (p *prompter) ask(ctx context.Context, label string, hidden bool) (credential, error) {
	fd := int(p.in.Fd())
	saved, err := term.GetState(fd)
	if err != nil {
		return credential{}, fmt.Errorf("read terminal state: %w", err)
	}
	_, _ = fmt.Fprintf(p.out, "%s: ", label)
	answers := make(chan answer, 1)
	go func() {
		if hidden {
			text, err := term.ReadPassword(fd)
			answers <- answer{string(text), err}
			return
		}
		text, err := readLine(p.in)
		answers <- answer{text, err}
	}()
	var a answer
	select {
	case <-ctx.Done():
		_ = term.Restore(fd, saved)
		_, _ = fmt.Fprintln(p.out)
		return credential{}, errInterrupted
	case a = <-answers:
	}
	if hidden {
		_, _ = fmt.Fprintln(p.out)
	}
	text := strings.TrimSpace(a.text)
	if a.err != nil && !errors.Is(a.err, io.EOF) {
		return credential{}, fmt.Errorf("read %s: %w", strings.ToLower(label), a.err)
	}
	if text == "" {
		return credential{}, fmt.Errorf("no %s entered, nothing was saved", strings.ToLower(label))
	}
	if hidden {
		text = a.text
	}
	return credential{value: text, source: "the " + strings.ToLower(label) + " you typed"}, nil
}

func readLine(r io.Reader) (string, error) {
	var line []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if n == 1 {
			if b[0] == '\n' {
				return strings.TrimSuffix(string(line), "\r"), nil
			}
			line = append(line, b[0])
		}
		if err != nil {
			return string(line), err
		}
	}
}
