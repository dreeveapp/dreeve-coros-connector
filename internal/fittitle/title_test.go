package fittitle_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"

	"github.com/xballoy/dreeve-coros-connector/internal/fittitle"
)

func TestWriteAddsTheTitleToEveryGoldenFileAndKeepsEveryOriginalMessage(t *testing.T) {
	for _, path := range goldenFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw := readFile(t, path)

			titled, err := fittitle.Write(raw, "Morning Run")
			if err != nil {
				t.Fatalf("Write: %v", err)
			}

			files := decode(t, titled)
			if got := workoutNames(files); len(got) != 1 || got[0] != "Morning Run" {
				t.Fatalf("workout names = %q, want [\"Morning Run\"]", got)
			}
			if got, want := dump(withoutWorkouts(files)), dump(decode(t, raw)); got != want {
				t.Fatalf("original messages changed")
			}
		})
	}
}

func TestWriteOnlyAppendsToTheDataAndKeepsEveryOriginalByte(t *testing.T) {
	for _, path := range goldenFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw := readFile(t, path)

			titled := write(t, raw, "Morning Run")

			headerSize := int(raw[0])
			dataEnd := len(raw) - 2
			if !bytes.Equal(titled[headerSize:dataEnd], raw[headerSize:dataEnd]) {
				t.Fatal("original records changed")
			}
			if !bytes.Equal(titled[1:4], raw[1:4]) || !bytes.Equal(titled[8:12], raw[8:12]) {
				t.Fatal("header fields other than the data size changed")
			}
		})
	}
}

var encodings = map[string][]encoder.Option{
	"little-endian":         {},
	"big-endian":            {encoder.WithBigEndian()},
	"compressed timestamps": {encoder.WithHeaderOption(encoder.HeaderOptionCompressedTimestamp, 0)},
}

func TestWriteHandlesEveryEncoding(t *testing.T) {
	for name, opts := range encodings {
		t.Run(name, func(t *testing.T) {
			raw := encode(t, opts, activity())

			files := decode(t, write(t, raw, "Tempo"))

			if got := workoutNames(files); len(got) != 1 || got[0] != "Tempo" {
				t.Fatalf("workout names = %q, want [\"Tempo\"]", got)
			}
			if got, want := dump(withoutWorkouts(files)), dump(decode(t, raw)); got != want {
				t.Fatalf("original messages changed")
			}
		})
	}
}

func TestWriteAddsTheTitleToTheLastChainedFileAndLeavesTheOthersUntouched(t *testing.T) {
	first := encode(t, nil, activity())
	raw := append(bytes.Clone(first), encode(t, nil, activity())...)

	titled := write(t, raw, "Brick")

	if !bytes.Equal(titled[:len(first)], first) {
		t.Fatal("first chained file changed")
	}
	files := decode(t, titled)
	if len(files) != 2 {
		t.Fatalf("got %d chained files, want 2", len(files))
	}
	if got := workoutNames(files[1:]); len(got) != 1 || got[0] != "Brick" {
		t.Fatalf("workout names in the last file = %q, want [\"Brick\"]", got)
	}
}

func TestWriteRefusesAFileThatAlreadyHasAWorkout(t *testing.T) {
	withWorkout := activity()
	withWorkout.Messages = append(withWorkout.Messages, workout("Planned intervals"))
	for name, raw := range map[string][]byte{
		"single file":        encode(t, nil, withWorkout),
		"earlier in a chain": encode(t, nil, withWorkout, activity()),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fittitle.Write(raw, "Morning Run"); !errors.Is(err, fittitle.ErrHasWorkout) {
				t.Fatalf("Write error = %v, want ErrHasWorkout", err)
			}
		})
	}
}

func TestWriteRefusesAnEmptyTitle(t *testing.T) {
	raw := encode(t, nil, activity())
	for _, title := range []string{"", "   ", "\x00"} {
		if _, err := fittitle.Write(raw, title); !errors.Is(err, fittitle.ErrEmptyTitle) {
			t.Errorf("Write(%q) error = %v, want ErrEmptyTitle", title, err)
		}
	}
}

func TestWriteTruncatesALongTitleOnACharacterBoundary(t *testing.T) {
	raw := encode(t, nil, activity())

	titled := write(t, raw, "a"+strings.Repeat("é", 200))

	want := "a" + strings.Repeat("é", 126)
	if got := workoutNames(decode(t, titled)); len(got) != 1 || got[0] != want {
		t.Fatalf("workout names = %q, want [%q]", got, want)
	}
	if got := encodedTitle(titled); got != want {
		t.Fatalf("encoded wkt_name = %q, want %q", got, want)
	}
}

func TestWriteKeepsATitleAtTheFieldLimit(t *testing.T) {
	raw := encode(t, nil, activity())
	title := strings.Repeat("x", fittitle.MaxTitleBytes)

	if got := workoutNames(decode(t, write(t, raw, title))); len(got) != 1 || got[0] != title {
		t.Fatalf("workout names = %q, want the %d-byte title", got, fittitle.MaxTitleBytes)
	}
}

func TestWriteRejectsInputThatIsNotAnIntactFITFile(t *testing.T) {
	valid := encode(t, nil, activity())
	badCRC := bytes.Clone(valid)
	badCRC[len(badCRC)-1] ^= 0xFF
	badHeaderCRC := bytes.Clone(valid)
	binary.LittleEndian.PutUint16(badHeaderCRC[12:], binary.LittleEndian.Uint16(badHeaderCRC[12:])^0xFFFF)
	for name, c := range map[string]struct {
		input []byte
		want  error
	}{
		"empty":            {nil, fittitle.ErrNotFIT},
		"not FIT":          {[]byte("FIT content of 900000000000000002"), fittitle.ErrNotFIT},
		"truncated":        {valid[:len(valid)-10], fittitle.ErrTruncated},
		"file CRC":         {badCRC, fittitle.ErrChecksum},
		"header CRC":       {badHeaderCRC, fittitle.ErrChecksum},
		"trailing garbage": {append(bytes.Clone(valid), 1, 2, 3), fittitle.ErrNotFIT},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fittitle.Write(c.input, "Morning Run"); !errors.Is(err, c.want) {
				t.Fatalf("Write error = %v, want %v", err, c.want)
			}
		})
	}
}

func FuzzWrite(f *testing.F) {
	for _, opts := range encodings {
		f.Add(encode(f, opts, activity()), "Morning Run")
	}
	f.Add(encode(f, nil, activity(), activity()), "a"+strings.Repeat("é", 200))
	f.Fuzz(func(t *testing.T, input []byte, title string) {
		titled, err := fittitle.Write(input, title)
		if err != nil {
			return
		}
		if len(titled) <= len(input) {
			t.Fatalf("titled file is %d bytes, want more than %d", len(titled), len(input))
		}
		lastStart := lastFileStart(input)
		if !bytes.Equal(titled[:lastStart+4], input[:lastStart+4]) {
			t.Fatal("bytes before the last data size changed")
		}
		if !bytes.Equal(titled[lastStart+8:lastStart+12], input[lastStart+8:lastStart+12]) {
			t.Fatal("last header's data type changed")
		}
		recordsStart := lastStart + int(input[lastStart])
		if !bytes.Equal(titled[recordsStart:len(input)-2], input[recordsStart:len(input)-2]) {
			t.Fatal("original records changed")
		}
		files, err := decodeStrict(titled)
		if err != nil {
			t.Fatalf("titled file does not decode with checksums enforced: %v", err)
		}
		names := workoutNames(files)
		if len(names) != 1 {
			t.Fatalf("workout names = %q, want one", names)
		}
		if encodedTitle(titled) != names[0] {
			t.Fatalf("encoded wkt_name %q differs from the decoded %q", encodedTitle(titled), names[0])
		}
		if !utf8.ValidString(names[0]) || len(names[0]) > fittitle.MaxTitleBytes || names[0] == "" {
			t.Fatalf("wkt_name %q is not a valid title", names[0])
		}
		if clean := strings.ReplaceAll(strings.ToValidUTF8(title, ""), "\x00", ""); !strings.HasPrefix(clean, names[0]) {
			t.Fatalf("wkt_name %q is not a prefix of the title %q", names[0], title)
		}
	})
}

func encodedTitle(titled []byte) string {
	field := titled[:len(titled)-3]
	start := bytes.LastIndexByte(field, 0) + 1
	return string(field[start:])
}

func lastFileStart(b []byte) int {
	start := 0
	for {
		end := start + int(b[start]) + int(binary.LittleEndian.Uint32(b[start+4:])) + 2
		if end >= len(b) {
			return start
		}
		start = end
	}
}

func activity() *proto.FIT {
	field := factory.CreateField
	record := func(ts uint32, hr uint8) proto.Message {
		return proto.Message{Num: mesgnum.Record, Fields: []proto.Field{
			field(mesgnum.Record, fieldnum.RecordTimestamp).WithValue(ts),
			field(mesgnum.Record, fieldnum.RecordHeartRate).WithValue(hr),
		}, DeveloperFields: []proto.DeveloperField{{Num: 0, DeveloperDataIndex: 0, Value: proto.Uint16(uint16(200 + hr))}}}
	}
	return &proto.FIT{Messages: []proto.Message{
		{Num: mesgnum.FileId, Fields: []proto.Field{
			field(mesgnum.FileId, fieldnum.FileIdType).WithValue(typedef.FileActivity.Byte()),
			field(mesgnum.FileId, fieldnum.FileIdManufacturer).WithValue(typedef.ManufacturerCoros.Uint16()),
			field(mesgnum.FileId, fieldnum.FileIdTimeCreated).WithValue(uint32(1100000000)),
		}},
		{Num: mesgnum.DeveloperDataId, Fields: []proto.Field{
			field(mesgnum.DeveloperDataId, fieldnum.DeveloperDataIdDeveloperDataIndex).WithValue(uint8(0)),
		}},
		{Num: mesgnum.FieldDescription, Fields: []proto.Field{
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionDeveloperDataIndex).WithValue(uint8(0)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFieldDefinitionNumber).WithValue(uint8(0)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFitBaseTypeId).WithValue(uint8(0x84)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFieldName).WithValue("Form Power"),
		}},
		record(1100000001, 120),
		record(1100000002, 130),
		{Num: mesgnum.Session, Fields: []proto.Field{
			field(mesgnum.Session, fieldnum.SessionTimestamp).WithValue(uint32(1100000002)),
		}},
	}}
}

func workout(name string) proto.Message {
	return proto.Message{Num: mesgnum.Workout, Fields: []proto.Field{
		factory.CreateField(mesgnum.Workout, fieldnum.WorkoutWktName).WithValue(name),
	}}
}

type tb interface {
	Helper()
	Fatalf(format string, args ...any)
}

func encode(t tb, opts []encoder.Option, files ...*proto.FIT) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := encoder.New(&buf, append([]encoder.Option{encoder.WithProtocolVersion(proto.V2)}, opts...)...)
	for _, f := range files {
		if err := enc.Encode(f); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	return buf.Bytes()
}

func write(t *testing.T, raw []byte, title string) []byte {
	t.Helper()
	out, err := fittitle.Write(raw, title)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return out
}

func goldenFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("../../testdata/fit/*.fit")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no golden FIT files: %v", err)
	}
	return paths
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decode(t *testing.T, b []byte) []*proto.FIT {
	t.Helper()
	files, err := decodeStrict(b)
	if err != nil {
		t.Fatalf("decode with checksums enforced: %v", err)
	}
	return files
}

func decodeStrict(b []byte) ([]*proto.FIT, error) {
	dec := decoder.New(bytes.NewReader(b))
	var files []*proto.FIT
	for dec.Next() {
		f, err := dec.Decode()
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no FIT file")
	}
	return files, nil
}

func workoutNames(files []*proto.FIT) []string {
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

func withoutWorkouts(files []*proto.FIT) []*proto.FIT {
	out := make([]*proto.FIT, 0, len(files))
	for _, f := range files {
		kept := *f
		kept.Messages = nil
		for _, m := range f.Messages {
			if m.Num != mesgnum.Workout {
				kept.Messages = append(kept.Messages, m)
			}
		}
		out = append(out, &kept)
	}
	return out
}

func dump(files []*proto.FIT) string {
	var b strings.Builder
	for i, file := range files {
		fmt.Fprintf(&b, "file %d protocol %d profile %d\n", i, file.FileHeader.ProtocolVersion, file.FileHeader.ProfileVersion)
		for _, m := range file.Messages {
			for _, f := range m.Fields {
				fmt.Fprintf(&b, "%s.%d %s=%v\n", m.Num, f.Num, f.Name, f.Value.Any())
			}
			for _, d := range m.DeveloperFields {
				fmt.Fprintf(&b, "%s dev %d.%d=%v\n", m.Num, d.DeveloperDataIndex, d.Num, d.Value.Any())
			}
		}
	}
	return b.String()
}
