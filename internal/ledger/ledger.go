package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/atomicfile"
)

const currentVersion = 1

type Status string

const (
	Delivered Status = "delivered"
	NoFile    Status = "no-file"
)

type Entry struct {
	Status     Status `json:"status"`
	SportType  int    `json:"sportType"`
	Name       string `json:"name,omitempty"`
	File       string `json:"file,omitempty"`
	ResultCode string `json:"resultCode,omitempty"`
	At         string `json:"at,omitempty"`
}

type Ledger struct {
	Version       int              `json:"version"`
	Since         string           `json:"since"`
	CompleteUntil string           `json:"completeUntil,omitempty"`
	Activities    map[string]Entry `json:"activities"`
}

func New() *Ledger {
	return &Ledger{Version: currentVersion, Activities: map[string]Entry{}}
}

func (l *Ledger) Has(labelID string) bool {
	_, ok := l.Activities[labelID]
	return ok
}

func (l *Ledger) Record(labelID string, e Entry) {
	l.Activities[labelID] = e
}

func (l *Ledger) Counts() map[Status]int {
	counts := map[Status]int{Delivered: 0, NoFile: 0}
	for _, e := range l.Activities {
		counts[e.Status]++
	}
	return counts
}

func Parse(data []byte) (*Ledger, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var l Ledger
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("decode ledger: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode ledger: unexpected data after the ledger object")
	}
	if l.Version != currentVersion {
		return nil, fmt.Errorf("unsupported ledger version %d", l.Version)
	}
	for field, value := range map[string]string{"since": l.Since, "completeUntil": l.CompleteUntil} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(time.DateOnly, value); err != nil {
			return nil, fmt.Errorf("invalid %s %q: %w", field, value, err)
		}
	}
	if l.Activities == nil {
		return nil, errors.New("ledger has no activities object")
	}
	for id, e := range l.Activities {
		switch e.Status {
		case Delivered, NoFile:
		default:
			return nil, fmt.Errorf("activity %s has unknown status %q", id, e.Status)
		}
	}
	return &l, nil
}

func Load(path string) (*Ledger, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read ledger %s: %w", path, err)
	}
	l, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("ledger %s is corrupt, refusing to continue: %w", path, err)
	}
	return l, nil
}

func (l *Ledger) Save(path string) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return fmt.Errorf("encode ledger: %w", err)
	}
	dir, name := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	return atomicfile.Write(dir, "."+name+".tmp", name, 0o644, bytes.NewReader(append(data, '\n')))
}
