package cli

import (
	"errors"

	"github.com/xballoy/dreeve-coros-connector/internal/daemon"
)

type tokenError struct {
	state daemon.TokenState
	err   error
}

func (e *tokenError) Error() string { return e.err.Error() }
func (e *tokenError) Unwrap() error { return e.err }

type fatalError struct{ err error }

func (e *fatalError) Error() string { return e.err.Error() }
func (e *fatalError) Unwrap() error { return e.err }

func reportFor(counts map[string]int, err error) (daemon.Report, error) {
	report := daemon.Report{Token: daemon.TokenValid, Counts: counts}
	if err == nil {
		return report, nil
	}
	var fatal *fatalError
	if errors.As(err, &fatal) {
		return report, err
	}
	report.Token, report.Err = daemon.TokenUnchanged, err
	var tokenErr *tokenError
	if errors.As(err, &tokenErr) {
		report.Token = tokenErr.state
	}
	return report, nil
}
