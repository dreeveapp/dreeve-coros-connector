package acceptance_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

func TestSyncOnceWritesTheCOROSTitleIntoEveryDeliveredFile(t *testing.T) {
	w := newWorld(t, 1, 1)
	golden := goldenFIT(t, "coros-outdoor-run.fit")
	a := w.addActivity(labelID(0), 1, withContent(golden), withName("Lunch Run ☀️"))
	w.mustRun("login")

	w.mustRun("sync-once")

	delivered := w.watchFiles()["coros_"+a.LabelID+".fit"]
	files := strictDecode(t, delivered)
	if got := wktNames(files); len(got) != 1 || got[0] != "Lunch Run ☀️" {
		t.Fatalf("delivered wkt_name = %q, want [\"Lunch Run ☀️\"]", got)
	}
	headerSize := int(golden[0])
	if !bytes.Equal(delivered[headerSize:len(golden)-2], golden[headerSize:len(golden)-2]) {
		t.Fatal("delivered file changed the records COROS served")
	}
}

func TestSyncOnceDeliversWhatCOROSServedWhenWriteTitleIsOff(t *testing.T) {
	w := newWorld(t, 1, 1)
	golden := goldenFIT(t, "coros-strength.fit")
	a := w.addActivity(labelID(0), 1, withContent(golden))
	w.env["WRITE_TITLE"] = "false"
	w.mustRun("login")

	w.mustRun("sync-once")

	if !bytes.Equal(w.watchFiles()["coros_"+a.LabelID+".fit"], golden) {
		t.Fatal("delivered file differs from what COROS served")
	}
}

func TestSyncOnceDeliversAFileWithAWorkoutUntouched(t *testing.T) {
	w := newWorld(t, 1, 1)
	served := fitWithWorkout(t, "Planned intervals")
	a := w.addActivity(labelID(0), 1, withContent(served))
	w.mustRun("login")

	r := w.mustRun("sync-once")

	if !bytes.Equal(w.watchFiles()["coros_"+a.LabelID+".fit"], served) {
		t.Fatal("delivered file differs from what COROS served")
	}
	assertContains(t, r.stderr, "already has a workout", a.LabelID)
}

func TestSyncOnceNeverDeliversAFileThatIsNotAValidFIT(t *testing.T) {
	golden := goldenFIT(t, "coros-outdoor-run.fit")
	badCRC := bytes.Clone(golden)
	badCRC[len(badCRC)-1] ^= 0xff
	contents := map[string]struct {
		served []byte
		reason string
	}{
		"an HTML error page": {[]byte("<html>Service Unavailable</html>"), "not a FIT file"},
		"a truncated file":   {golden[:len(golden)/2], "truncated FIT file"},
		"a wrong checksum":   {badCRC, "checksum mismatch"},
	}
	for name, c := range contents {
		for _, writeTitle := range []string{"true", "false"} {
			t.Run(name+" with WRITE_TITLE="+writeTitle, func(t *testing.T) {
				w := newWorld(t, 1, 1)
				w.env["WRITE_TITLE"] = writeTitle
				w.env["MAX_ATTEMPTS"] = "2"
				a := w.addActivity(labelID(0), 1, withContent(c.served))
				w.mustRun("login")

				w.mustRun("sync-once")

				w.assertNoFileInWatch()
				if e := w.ledger().Activities[a.LabelID]; e.Status != "pending" || e.Attempts != 1 || !strings.Contains(e.Error, c.reason) {
					t.Fatalf("after the first attempt, ledger entry = %+v, want pending with %q", e, c.reason)
				}

				w.mustRun("sync-once")

				w.assertNoFileInWatch()
				if e := w.ledger().Activities[a.LabelID]; e.Status != "failed" {
					t.Fatalf("after the last attempt, ledger entry = %+v, want failed", e)
				}
			})
		}
	}
}

func TestSyncOnceDeliversAnActivityWithoutANameUntouched(t *testing.T) {
	w := newWorld(t, 1, 1)
	golden := goldenFIT(t, "coros-indoor-run.fit")
	a := w.addActivity(labelID(0), 1, withContent(golden), withName(""))
	w.mustRun("login")

	r := w.mustRun("sync-once")

	if !bytes.Equal(w.watchFiles()["coros_"+a.LabelID+".fit"], golden) {
		t.Fatal("delivered file differs from what COROS served")
	}
	assertContains(t, r.stderr, "empty title", a.LabelID)
}

func TestSyncOnceRejectsAnInvalidWriteTitle(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.env["WRITE_TITLE"] = "sometimes"
	w.mustRun("login")

	r := w.run("sync-once")

	if r.code == 0 {
		t.Fatal("sync-once accepted WRITE_TITLE=sometimes")
	}
	assertContains(t, r.stderr, "WRITE_TITLE", "sometimes")
}

func withContent(content []byte) func(*fakecoros.Activity) {
	return func(a *fakecoros.Activity) { a.Content = content }
}

func withName(name string) func(*fakecoros.Activity) {
	return func(a *fakecoros.Activity) { a.Name = name }
}

func goldenFIT(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "fit", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func strictDecode(t *testing.T, b []byte) []*proto.FIT {
	t.Helper()
	dec := decoder.New(bytes.NewReader(b))
	var files []*proto.FIT
	for dec.Next() {
		f, err := dec.Decode()
		if err != nil {
			t.Fatalf("decode with checksums enforced: %v", err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no FIT file")
	}
	return files
}

func wktNames(files []*proto.FIT) []string {
	var names []string
	for _, f := range files {
		for _, m := range f.Messages {
			if m.Num != mesgnum.Workout {
				continue
			}
			for _, field := range m.Fields {
				if field.Name == "wkt_name" {
					names = append(names, field.Value.String())
				}
			}
		}
	}
	return names
}
