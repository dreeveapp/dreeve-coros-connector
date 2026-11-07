package ledger_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/ledger"
)

func TestLoadMissingFileReturnsEmptyLedger(t *testing.T) {
	l, err := ledger.Load(filepath.Join(t.TempDir(), "ledger.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if l.Since != "" || len(l.Activities) != 0 {
		t.Fatalf("expected empty ledger, got %+v", l)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l := ledger.New()
	l.Since = "2026-10-08"
	l.CompleteUntil = "2026-11-07"
	l.Record("900000000000000002", ledger.Entry{Status: ledger.Delivered, SportType: 101, File: "coros_900000000000000002.fit"})
	l.Record("900000000000000003", ledger.Entry{Status: ledger.NoFile, SportType: 900, ResultCode: "1001", Attempts: 5})
	l.Record("900000000000000005", ledger.Entry{Status: ledger.Pending, SportType: 100, Attempts: 2, Error: "COROS file download returned HTTP 503"})
	l.Record("900000000000000006", ledger.Entry{Status: ledger.Failed, SportType: 100, Attempts: 5, Error: "COROS file download returned HTTP 503"})
	l.Backoff = &ledger.Backoff{Until: time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC), Count: 2}

	if err := l.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := ledger.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, l) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, l)
	}
	if !got.Has("900000000000000002") || got.Has("900000000000000004") {
		t.Fatalf("Has reports wrong membership: %+v", got.Activities)
	}
}

func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	if err := ledger.New().Save(filepath.Join(dir, "ledger.json")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ledger.json" {
		t.Fatalf("unexpected files in state dir: %v", entries)
	}
}

func TestParseRejectsCorruptLedgers(t *testing.T) {
	cases := map[string]string{
		"empty":             ``,
		"truncated":         `{"version":1,"since":"2026-10-08","activities":{`,
		"not an object":     `[]`,
		"null":              `null`,
		"missing version":   `{"since":"2026-10-08","activities":{}}`,
		"unknown version":   `{"version":2,"since":"2026-10-08","activities":{}}`,
		"bad since":         `{"version":1,"since":"yesterday","activities":{}}`,
		"bad completeUntil": `{"version":1,"since":"2026-10-08","completeUntil":"soon","activities":{}}`,
		"unknown status":    `{"version":1,"since":"2026-10-08","activities":{"1":{"status":"lost"}}}`,
		"backoff no count":  `{"version":1,"since":"2026-10-08","activities":{},"backoff":{"until":"2026-11-07T09:30:00Z","count":0}}`,
		"backoff no until":  `{"version":1,"since":"2026-10-08","activities":{},"backoff":{"count":1}}`,
		"backoff bad until": `{"version":1,"since":"2026-10-08","activities":{},"backoff":{"until":"later","count":1}}`,
		"trailing garbage":  `{"version":1,"since":"2026-10-08","activities":{}} {}`,
		"unknown field":     `{"version":1,"since":"2026-10-08","activities":{},"extra":true}`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ledger.Parse([]byte(input)); err == nil {
				t.Fatalf("expected an error for %q", input)
			}
		})
	}
}

func TestLoadCorruptFileFailsAndLeavesItUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	corrupt := []byte(`{"version":1,"activities":{"9":`)
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.Load(path); err == nil {
		t.Fatal("expected Load to fail on a corrupt ledger")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt ledger was modified: %q", after)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(`{"version":1,"since":"2026-10-08","activities":{"900000000000000002":{"status":"delivered","sportType":101,"file":"coros_900000000000000002.fit"}}}`))
	f.Add([]byte(`{"version":1,"since":"","activities":{}}`))
	f.Add([]byte(`{"version":1,"since":"2026-10-08","backoff":{"until":"2026-11-07T09:30:00+02:00","count":2},"activities":{"9":{"status":"pending","attempts":1,"error":"HTTP 503"}}}`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`{"version":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(t.TempDir(), "ledger.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		loaded, loadErr := ledger.Load(path)
		if loadErr == nil && len(loaded.Activities) == 0 && loaded.Since == "" && !json.Valid(data) {
			t.Fatalf("Load silently reset a corrupt ledger %q", data)
		}

		l, err := ledger.Parse(data)
		if (err == nil) != (loadErr == nil) {
			t.Fatalf("Parse and Load disagree on %q: %v vs %v", data, err, loadErr)
		}
		if err != nil {
			return
		}
		if !json.Valid(data) {
			t.Fatalf("accepted invalid JSON %q", data)
		}
		encoded, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("marshal accepted ledger: %v", err)
		}
		again, err := ledger.Parse(encoded)
		if err != nil {
			t.Fatalf("re-parse of accepted ledger failed: %v", err)
		}
		if !reflect.DeepEqual(again, l) {
			t.Fatalf("accepted ledger does not round trip:\n got %+v\nwant %+v", again, l)
		}
	})
}

func TestCountsListsEveryStatusIncludingEmptyOnes(t *testing.T) {
	l := ledger.New()
	l.Record("900000000000000002", ledger.Entry{Status: ledger.Delivered})
	l.Record("900000000000000003", ledger.Entry{Status: ledger.Delivered})

	l.Record("900000000000000004", ledger.Entry{Status: ledger.Pending, Attempts: 1})

	want := map[ledger.Status]int{ledger.Delivered: 2, ledger.NoFile: 0, ledger.Pending: 1, ledger.Failed: 0}
	if got := l.Counts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Counts() = %v, want %v", got, want)
	}
}
