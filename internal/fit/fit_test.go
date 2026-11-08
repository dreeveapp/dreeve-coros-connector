package fit_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/kit/hash/crc16"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"

	"github.com/xballoy/dreeve-coros-connector/internal/fit"
)

var encodings = map[string][]encoder.Option{
	"little-endian":         {},
	"big-endian":            {encoder.WithBigEndian()},
	"compressed timestamps": {encoder.WithHeaderOption(encoder.HeaderOptionCompressedTimestamp, 0)},
}

func TestWalkVisitsEveryMessageAndFieldTheDecoderReads(t *testing.T) {
	inputs := map[string][]byte{}
	for _, path := range goldenFiles(t) {
		inputs[filepath.Base(path)] = readFile(t, path)
	}
	for name, opts := range encodings {
		inputs[name] = encode(t, opts, activity(120), activity(130))
	}
	for name, raw := range inputs {
		t.Run(name, func(t *testing.T) {
			var got []string
			files, err := fit.Walk(raw, func(m fit.Message) error {
				got = append(got, describe(m))
				return nil
			})
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}

			decoded := decode(t, raw)
			if len(files) != len(decoded) {
				t.Fatalf("Walk found %d files, the decoder %d", len(files), len(decoded))
			}
			var want []string
			for _, f := range decoded {
				for _, m := range f.Messages {
					want = append(want, describeDecoded(m))
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Walk visited\n%q\nthe decoder read\n%q", got, want)
			}
		})
	}
}

func TestWalkLocatesEveryFieldInTheBytes(t *testing.T) {
	for name, opts := range encodings {
		t.Run(name, func(t *testing.T) {
			raw := encode(t, opts, activity(120), activity(130))

			var heartRates []byte
			_, err := fit.Walk(raw, func(m fit.Message) error {
				for _, f := range m.Fields {
					if m.Num == mesgnum.Record && f.Num == fieldnum.RecordHeartRate {
						heartRates = append(heartRates, raw[f.Offset:f.Offset+f.Size]...)
					}
					if m.Num == mesgnum.Record && f.Num == fieldnum.RecordTimestamp && f.Order.Uint32(raw[f.Offset:]) < 1100000001 {
						return fmt.Errorf("timestamp %d read at the wrong offset or order", f.Order.Uint32(raw[f.Offset:]))
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}

			if want := []byte{120, 121, 130, 131}; !bytes.Equal(heartRates, want) {
				t.Fatalf("heart rate bytes = %v, want %v", heartRates, want)
			}
		})
	}
}

func TestWalkStopsWithTheVisitorError(t *testing.T) {
	stop := errors.New("stop")

	_, err := fit.Walk(encode(t, nil, activity(120)), func(fit.Message) error { return stop })

	if !errors.Is(err, stop) {
		t.Fatalf("Walk error = %v, want the visitor error", err)
	}
}

func TestWalkAcceptsEveryHeaderLayout(t *testing.T) {
	for name, raw := range headerLayouts(t, encode(t, nil, activity(120))) {
		t.Run(name, func(t *testing.T) {
			files, err := fit.Walk(raw, ignore)
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}
			if len(files) != 1 || files[0].Start != 0 || files[0].HeaderSize != int(raw[0]) || files[0].End != len(raw)-2 {
				t.Fatalf("files = %+v, want one file of %d bytes with a %d-byte header", files, len(raw), raw[0])
			}
		})
	}
}

func TestWalkRejectsInputThatIsNotAnIntactFITFile(t *testing.T) {
	valid := encode(t, nil, activity(120))
	badCRC := bytes.Clone(valid)
	badCRC[len(badCRC)-1] ^= 0xFF
	badHeaderCRC := bytes.Clone(valid)
	binary.LittleEndian.PutUint16(badHeaderCRC[12:], binary.LittleEndian.Uint16(badHeaderCRC[12:])^0xFFFF)
	undefined := sealed(t, append(bytes.Clone(valid[:14]), 0x03, 0x00, 0x00))
	for name, c := range map[string]struct {
		input []byte
		want  error
	}{
		"empty":                      {nil, fit.ErrNotFIT},
		"not FIT":                    {[]byte("FIT content of 900000000000000002"), fit.ErrNotFIT},
		"truncated":                  {valid[:len(valid)-10], fit.ErrTruncated},
		"file CRC":                   {badCRC, fit.ErrChecksum},
		"header CRC":                 {badHeaderCRC, fit.ErrChecksum},
		"trailing garbage":           {append(bytes.Clone(valid), 1, 2, 3), fit.ErrNotFIT},
		"data before its definition": {undefined, fit.ErrMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fit.Walk(c.input, ignore); !errors.Is(err, c.want) {
				t.Fatalf("Walk error = %v, want %v", err, c.want)
			}
		})
	}
}

func TestResealMakesEveryHeaderLayoutValidAgainAfterAnEdit(t *testing.T) {
	for name, raw := range headerLayouts(t, encode(t, nil, activity(120))) {
		t.Run(name, func(t *testing.T) {
			edited := bytes.Clone(raw)
			files, err := fit.Walk(edited, func(m fit.Message) error {
				for _, f := range m.Fields {
					if m.Num == mesgnum.Record && f.Num == fieldnum.RecordHeartRate {
						edited[f.Offset] = 99
					}
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}

			fit.Reseal(edited, files)

			if !bytes.Equal(edited[:raw[0]], raw[:raw[0]]) {
				t.Fatal("Reseal changed the header")
			}
			if got := heartRates(decode(t, edited)); !reflect.DeepEqual(got, []uint8{99, 99}) {
				t.Fatalf("heart rates = %v, want [99 99]", got)
			}
		})
	}
}

func TestAppendAddsRecordsToAFileAndKeepsTheOthers(t *testing.T) {
	for name, raw := range headerLayouts(t, encode(t, nil, activity(120))) {
		t.Run(name, func(t *testing.T) {
			chain := append(bytes.Clone(raw), encode(t, nil, activity(130))...)
			files, err := fit.Walk(chain, ignore)
			if err != nil {
				t.Fatalf("Walk: %v", err)
			}

			out, err := fit.Append(chain, files[0], workoutRecords("Tempo"))
			if err != nil {
				t.Fatalf("Append: %v", err)
			}

			decoded := decode(t, out)
			if len(decoded) != 2 {
				t.Fatalf("got %d files, want 2", len(decoded))
			}
			if got := workoutNames(decoded[0]); !reflect.DeepEqual(got, []string{"Tempo"}) {
				t.Fatalf("workout names in the first file = %q, want [\"Tempo\"]", got)
			}
			if !bytes.Equal(out[len(out)-(len(chain)-len(raw)):], chain[len(raw):]) {
				t.Fatal("Append changed the following file")
			}
			if !bytes.Equal(out[raw[0]:len(raw)-2], raw[raw[0]:len(raw)-2]) {
				t.Fatal("Append changed the existing records")
			}
		})
	}
}

func ignore(fit.Message) error { return nil }

func workoutRecords(name string) []byte {
	records := []byte{0x40, 0, 0, byte(mesgnum.Workout), 0, 1, byte(fieldnum.WorkoutWktName), byte(len(name) + 1), 0x07, 0x00}
	records = append(records, name...)
	return append(records, 0)
}

func describe(m fit.Message) string {
	s := fmt.Sprint(m.Num)
	for _, f := range m.Fields {
		if f.Num != proto.FieldNumTimestamp {
			s += fmt.Sprintf(" %d", f.Num)
		}
	}
	return s
}

func describeDecoded(m proto.Message) string {
	s := fmt.Sprint(m.Num)
	for _, f := range m.Fields {
		if f.Num != proto.FieldNumTimestamp {
			s += fmt.Sprintf(" %d", f.Num)
		}
	}
	return s
}

func sealed(t *testing.T, b []byte) []byte {
	t.Helper()
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-14))
	binary.LittleEndian.PutUint16(b[12:], crc(b[:12]))
	return binary.LittleEndian.AppendUint16(b, crc(b[14:]))
}

func headerLayouts(t *testing.T, fourteen []byte) map[string][]byte {
	t.Helper()
	if fourteen[0] != 14 {
		t.Fatalf("encoder wrote a %d-byte header, want 14", fourteen[0])
	}
	data := fourteen[14 : len(fourteen)-2]

	twelve := append([]byte{12}, fourteen[1:12]...)
	twelve = append(twelve, data...)
	twelve = binary.LittleEndian.AppendUint16(twelve, crc(twelve))

	zero := append(bytes.Clone(fourteen[:12]), 0, 0)
	zero = append(zero, data...)
	zero = binary.LittleEndian.AppendUint16(zero, crc(data))

	return map[string][]byte{"14-byte": fourteen, "12-byte": twelve, "14-byte with no header CRC": zero}
}

func crc(b []byte) uint16 {
	h := crc16.New()
	_, _ = h.Write(b)
	return h.Sum16()
}

func activity(hr uint8) *proto.FIT {
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
		record(1100000001, hr),
		record(1100000002, hr+1),
	}}
}

func encode(t *testing.T, opts []encoder.Option, files ...*proto.FIT) []byte {
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
	dec := decoder.New(bytes.NewReader(b), decoder.WithNoComponentExpansion())
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

func heartRates(files []*proto.FIT) []uint8 {
	var hrs []uint8
	for _, f := range files {
		for _, m := range f.Messages {
			if m.Num == mesgnum.Record {
				hrs = append(hrs, m.FieldValueByNum(fieldnum.RecordHeartRate).Uint8())
			}
		}
	}
	return hrs
}

func workoutNames(f *proto.FIT) []string {
	var names []string
	for _, m := range f.Messages {
		if m.Num == mesgnum.Workout {
			names = append(names, m.FieldValueByNum(fieldnum.WorkoutWktName).String())
		}
	}
	return names
}
