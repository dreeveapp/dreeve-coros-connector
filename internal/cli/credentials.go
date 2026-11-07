package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
)

type credential struct {
	value  string
	source string
}

func credentials(ctx context.Context, getenv Env, a *app) (email, password credential, err error) {
	if email, err = credentialFromEnv(getenv, "COROS_EMAIL"); err != nil {
		return email, password, err
	}
	password, err = credentialFromEnv(getenv, "COROS_PASSWORD")
	a.addPassword(password.value)
	if err != nil {
		return email, password, err
	}
	if a.prompt == nil {
		return email, password, missingCredentials(email, password)
	}
	if email.value == "" {
		if email, err = a.prompt.askLine(ctx, "Email"); err != nil {
			return email, password, err
		}
	}
	if password.value == "" {
		password, err = a.prompt.askHidden(ctx, "Password")
		a.addPassword(password.value)
	}
	return email, password, err
}

func (a *app) addPassword(password string) {
	if password == "" {
		return
	}
	a.secrets.Add(password)
	a.secrets.Add(coros.PasswordHash(password))
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

func missingCredentials(email, password credential) error {
	var missing []string
	if email.value == "" {
		missing = append(missing, "COROS_EMAIL or COROS_EMAIL_FILE")
	}
	if password.value == "" {
		missing = append(missing, "COROS_PASSWORD or COROS_PASSWORD_FILE")
	}
	if len(missing) == 0 {
		return nil
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

func (p *prompter) askLine(ctx context.Context, label string) (credential, error) {
	text, err := p.ask(ctx, label, func() (string, error) { return readLine(p.in) })
	return credential{value: strings.TrimSpace(text), source: typedSource(label)}, err
}

func (p *prompter) askHidden(ctx context.Context, label string) (credential, error) {
	text, err := p.ask(ctx, label, func() (string, error) {
		b, err := term.ReadPassword(int(p.in.Fd()))
		return string(b), err
	})
	if !errors.Is(err, errInterrupted) {
		_, _ = fmt.Fprintln(p.out)
	}
	return credential{value: text, source: typedSource(label)}, err
}

type answer struct {
	text string
	err  error
}

func (p *prompter) ask(ctx context.Context, label string, read func() (string, error)) (string, error) {
	fd := int(p.in.Fd())
	saved, err := term.GetState(fd)
	if err != nil {
		return "", fmt.Errorf("read terminal state: %w", err)
	}
	_, _ = fmt.Fprintf(p.out, "%s: ", label)
	answers := make(chan answer, 1)
	go func() {
		text, err := read()
		answers <- answer{text, err}
	}()
	select {
	case <-ctx.Done():
		_ = term.Restore(fd, saved)
		_, _ = fmt.Fprintln(p.out)
		return "", errInterrupted
	case a := <-answers:
		if a.err != nil && !errors.Is(a.err, io.EOF) {
			return "", fmt.Errorf("read %s: %w", strings.ToLower(label), a.err)
		}
		if strings.TrimSpace(a.text) == "" {
			return "", fmt.Errorf("no %s entered, nothing was saved", strings.ToLower(label))
		}
		return a.text, nil
	}
}

func typedSource(label string) string { return "the " + strings.ToLower(label) + " you typed" }

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
