package acceptance_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
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

func TestSyncOnceDeliversAFileItCannotTitleUntouchedAndWarns(t *testing.T) {
	w := newWorld(t, 1, 1)
	a := w.addActivity(labelID(0), 1, withContent([]byte("an HTML error page")))
	w.mustRun("login")

	r := w.mustRun("sync-once")

	if got := w.watchFiles()["coros_"+a.LabelID+".fit"]; string(got) != "an HTML error page" {
		t.Fatalf("delivered %q, want what COROS served", got)
	}
	assertContains(t, r.stderr, "WARN", "title not written", a.LabelID, "not a FIT file")
	if e := w.ledger().Activities[a.LabelID]; e.Status != "delivered" {
		t.Fatalf("ledger status = %q, want delivered", e.Status)
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

func fitWithWorkout(t *testing.T, name string) []byte {
	t.Helper()
	field := factory.CreateField
	fit := &proto.FIT{Messages: []proto.Message{
		{Num: mesgnum.FileId, Fields: []proto.Field{
			field(mesgnum.FileId, fieldnum.FileIdType).WithValue(typedef.FileActivity.Byte()),
			field(mesgnum.FileId, fieldnum.FileIdManufacturer).WithValue(typedef.ManufacturerCoros.Uint16()),
		}},
		{Num: mesgnum.Workout, Fields: []proto.Field{
			field(mesgnum.Workout, fieldnum.WorkoutWktName).WithValue(name),
		}},
		{Num: mesgnum.Record, Fields: []proto.Field{
			field(mesgnum.Record, fieldnum.RecordTimestamp).WithValue(uint32(1100000001)),
			field(mesgnum.Record, fieldnum.RecordHeartRate).WithValue(uint8(120)),
		}},
	}}
	var buf bytes.Buffer
	if err := encoder.New(&buf).Encode(fit); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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
