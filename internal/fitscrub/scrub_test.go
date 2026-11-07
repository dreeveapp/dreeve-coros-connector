package fitscrub_test

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/basetype"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"

	"github.com/xballoy/dreeve-coros-connector/internal/fitscrub"
)

var origin = fitscrub.Origin{Lat: 45.5, Long: 5.25}

const invalidSemicircle = math.MaxInt32

var encodings = map[string][]encoder.Option{
	"little-endian":         {},
	"big-endian":            {encoder.WithBigEndian()},
	"compressed timestamps": {encoder.WithHeaderOption(encoder.HeaderOptionCompressedTimestamp, 0)},
}

func TestScrubMovesTheFirstPositionToTheOriginAndKeepsTheTrackShape(t *testing.T) {
	for name, opts := range encodings {
		t.Run(name, func(t *testing.T) {
			raw := encode(t, opts, activity(48.85, 2.29))

			scrubbed := scrub(t, raw)

			assertTranslatedToOrigin(t, positions(decode(t, raw)), positions(decode(t, scrubbed)))
		})
	}
}

func TestScrubAppliesOneTranslationAcrossChainedFiles(t *testing.T) {
	raw := encode(t, nil, activity(48.85, 2.29), activity(48.90, 2.35))

	scrubbed := scrub(t, raw)

	files := decode(t, scrubbed)
	if len(files) != 2 {
		t.Fatalf("got %d chained files, want 2", len(files))
	}
	assertTranslatedToOrigin(t, positions(decode(t, raw)), positions(files))
}

func TestScrubCrossesTheAntimeridianWithoutLosingTheTrackShape(t *testing.T) {
	raw := encode(t, nil, activity(-17.7, 179.99))

	scrubbed := scrub(t, raw)

	assertTranslatedToOrigin(t, positions(decode(t, raw)), positions(decode(t, scrubbed)))
}

func TestScrubLeavesMissingPositionsMissing(t *testing.T) {
	raw := encode(t, nil, activity(48.85, 2.29))

	for _, p := range positions(decode(t, scrub(t, raw))) {
		if p.missingBefore(decode(t, raw)) && p.value != invalidSemicircle {
			t.Errorf("%s became %d, want it to stay missing", p.name, p.value)
		}
	}
}

func TestScrubReplacesEverySerialNumber(t *testing.T) {
	raw := encode(t, nil, activity(48.85, 2.29))

	var seen int
	for _, m := range messages(decode(t, scrub(t, raw))) {
		for _, f := range m.Fields {
			if f.Name == "serial_number" {
				seen++
				if got := f.Value.Uint32z(); got != fitscrub.SerialNumber {
					t.Errorf("%s serial_number = %d, want %d", m.Num, got, fitscrub.SerialNumber)
				}
			}
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d serial numbers, want 2", seen)
	}
}

func TestScrubClearsTheUserProfile(t *testing.T) {
	raw := encode(t, nil, activity(48.85, 2.29))

	var seen int
	for _, m := range messages(decode(t, scrub(t, raw))) {
		if m.Num != mesgnum.UserProfile {
			continue
		}
		for _, f := range m.Fields {
			seen++
			if f.Value.Valid(f.BaseType) {
				t.Errorf("user_profile %s = %v, want no value", f.Name, f.Value.Any())
			}
		}
	}
	if seen == 0 {
		t.Fatal("saw no user_profile field")
	}
}

func TestScrubKeepsEveryOtherFieldAndTheFileSize(t *testing.T) {
	for name, opts := range encodings {
		t.Run(name, func(t *testing.T) {
			raw := encode(t, opts, activity(48.85, 2.29), activity(48.90, 2.35))

			scrubbed := scrub(t, raw)

			if len(scrubbed) != len(raw) {
				t.Errorf("scrubbed file is %d bytes, want %d", len(scrubbed), len(raw))
			}
			before, after := unscrubbedFields(decode(t, raw)), unscrubbedFields(decode(t, scrubbed))
			if before != after {
				t.Errorf("fields outside the scrubbed ones changed:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
}

func TestScrubRejectsInputThatIsNotAnIntactFITFile(t *testing.T) {
	raw := encode(t, nil, activity(48.85, 2.29))
	corrupted := bytes.Clone(raw)
	corrupted[len(corrupted)/2] ^= 0xFF

	for name, input := range map[string][]byte{
		"empty":       {},
		"not FIT":     []byte("definitely not a FIT file at all"),
		"truncated":   raw[:len(raw)-5],
		"corrupted":   corrupted,
		"no data CRC": raw[:len(raw)-2],
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := fitscrub.Scrub(input, origin); err == nil {
				t.Error("Scrub accepted it, want an error")
			}
		})
	}
}

func FuzzScrub(f *testing.F) {
	for _, opts := range encodings {
		f.Add(encode(f, opts, activity(48.85, 2.29), activity(-17.7, 179.99)))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		scrubbed, err := fitscrub.Scrub(input, origin)
		if err != nil {
			return
		}
		if len(scrubbed) != len(input) {
			t.Fatalf("scrubbed file is %d bytes, want %d", len(scrubbed), len(input))
		}
		if _, err := decodeStrict(scrubbed); err != nil {
			t.Fatalf("scrubbed file does not decode: %v", err)
		}
	})
}

func activity(lat, long float64) *proto.FIT {
	start := semicircles(lat)
	east := semicircles(long)
	field := factory.CreateField
	unknown := proto.Field{FieldBase: &proto.FieldBase{Name: "unknown", Num: 200, BaseType: basetype.Uint8, Scale: 1}, Value: proto.Uint8(7)}
	record := func(ts uint32, la, lo int32, hr uint8) proto.Message {
		return proto.Message{Num: mesgnum.Record, Fields: []proto.Field{
			field(mesgnum.Record, fieldnum.RecordTimestamp).WithValue(ts),
			field(mesgnum.Record, fieldnum.RecordPositionLat).WithValue(la),
			field(mesgnum.Record, fieldnum.RecordPositionLong).WithValue(lo),
			field(mesgnum.Record, fieldnum.RecordHeartRate).WithValue(hr),
			unknown,
		}, DeveloperFields: []proto.DeveloperField{{Num: 0, DeveloperDataIndex: 0, Value: proto.Uint16(uint16(200 + hr))}}}
	}
	return &proto.FIT{Messages: []proto.Message{
		{Num: mesgnum.FileId, Fields: []proto.Field{
			field(mesgnum.FileId, fieldnum.FileIdType).WithValue(typedef.FileActivity.Byte()),
			field(mesgnum.FileId, fieldnum.FileIdManufacturer).WithValue(typedef.ManufacturerCoros.Uint16()),
			field(mesgnum.FileId, fieldnum.FileIdSerialNumber).WithValue(uint32(3456789012)),
			field(mesgnum.FileId, fieldnum.FileIdTimeCreated).WithValue(uint32(1100000000)),
		}},
		{Num: mesgnum.DeveloperDataId, Fields: []proto.Field{
			field(mesgnum.DeveloperDataId, fieldnum.DeveloperDataIdDeveloperDataIndex).WithValue(uint8(0)),
			field(mesgnum.DeveloperDataId, fieldnum.DeveloperDataIdApplicationId).WithValue([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}),
		}},
		{Num: mesgnum.FieldDescription, Fields: []proto.Field{
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionDeveloperDataIndex).WithValue(uint8(0)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFieldDefinitionNumber).WithValue(uint8(0)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFitBaseTypeId).WithValue(uint8(basetype.Uint16)),
			field(mesgnum.FieldDescription, fieldnum.FieldDescriptionFieldName).WithValue("stryd_power"),
		}},
		{Num: mesgnum.UserProfile, Fields: []proto.Field{
			field(mesgnum.UserProfile, fieldnum.UserProfileFriendlyName).WithValue("Jane Runner"),
			field(mesgnum.UserProfile, fieldnum.UserProfileGender).WithValue(typedef.GenderFemale.Byte()),
			field(mesgnum.UserProfile, fieldnum.UserProfileAge).WithValue(uint8(41)),
			field(mesgnum.UserProfile, fieldnum.UserProfileWeight).WithValue(uint16(6250)),
		}},
		{Num: mesgnum.DeviceInfo, Fields: []proto.Field{
			field(mesgnum.DeviceInfo, fieldnum.DeviceInfoTimestamp).WithValue(uint32(1100000000)),
			field(mesgnum.DeviceInfo, fieldnum.DeviceInfoSerialNumber).WithValue(uint32(2345678901)),
			field(mesgnum.DeviceInfo, fieldnum.DeviceInfoProductName).WithValue("PACE 3"),
		}},
		{Num: typedef.MesgNum(0xFF01), Fields: []proto.Field{unknown}},
		record(1100000001, invalidSemicircle, invalidSemicircle, 90),
		record(1100000002, start, east, 120),
		record(1100000003, start+4000, east-9000, 130),
		record(1100000004, start-2500, east+7000, 140),
		{Num: mesgnum.Lap, Fields: []proto.Field{
			field(mesgnum.Lap, fieldnum.LapTimestamp).WithValue(uint32(1100000004)),
			field(mesgnum.Lap, fieldnum.LapStartPositionLat).WithValue(start),
			field(mesgnum.Lap, fieldnum.LapStartPositionLong).WithValue(east),
		}},
		{Num: mesgnum.Session, Fields: []proto.Field{
			field(mesgnum.Session, fieldnum.SessionTimestamp).WithValue(uint32(1100000004)),
			field(mesgnum.Session, fieldnum.SessionStartPositionLat).WithValue(start),
			field(mesgnum.Session, fieldnum.SessionStartPositionLong).WithValue(east),
			field(mesgnum.Session, fieldnum.SessionNecLat).WithValue(start + 4000),
			field(mesgnum.Session, fieldnum.SessionNecLong).WithValue(east + 7000),
			field(mesgnum.Session, fieldnum.SessionSwcLat).WithValue(start - 2500),
			field(mesgnum.Session, fieldnum.SessionSwcLong).WithValue(east - 9000),
		}},
	}}
}

func semicircles(degrees float64) int32 {
	return int32(degrees * (1 << 31) / 180)
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

func scrub(t *testing.T, raw []byte) []byte {
	t.Helper()
	out, err := fitscrub.Scrub(raw, origin)
	if err != nil {
		t.Fatalf("Scrub: %v", err)
	}
	return out
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
	dec := decoder.New(bytes.NewReader(b), decoder.WithNoComponentExpansion())
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

func messages(files []*proto.FIT) []proto.Message {
	var all []proto.Message
	for _, f := range files {
		all = append(all, f.Messages...)
	}
	return all
}

type position struct {
	index int
	name  string
	lat   bool
	value int32
}

func (p position) missingBefore(raw []*proto.FIT) bool {
	return positions(raw)[p.index].value == invalidSemicircle
}

func positions(files []*proto.FIT) []position {
	var all []position
	for _, m := range messages(files) {
		for _, f := range m.Fields {
			if f.Units == "semicircles" {
				all = append(all, position{
					index: len(all),
					name:  fmt.Sprintf("%s.%s", m.Num, f.Name),
					lat:   strings.HasSuffix(f.Name, "_lat"),
					value: f.Value.Int32(),
				})
			}
		}
	}
	return all
}

func assertTranslatedToOrigin(t *testing.T, before, after []position) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("got %d positions, want %d", len(after), len(before))
	}
	firstLat, firstLong := firstValid(before, true), firstValid(before, false)
	originLat, originLong := semicircles(origin.Lat), semicircles(origin.Long)
	if got := firstValid(after, true); got != originLat {
		t.Errorf("first latitude = %d, want the origin %d", got, originLat)
	}
	if got := firstValid(after, false); got != originLong {
		t.Errorf("first longitude = %d, want the origin %d", got, originLong)
	}
	for i, b := range before {
		if b.value == invalidSemicircle {
			continue
		}
		first, o := firstLong, originLong
		if b.lat {
			first, o = firstLat, originLat
		}
		if got, want := after[i].value-o, b.value-first; got != want {
			t.Errorf("%s is %d from the origin, want %d as it was from the first position", b.name, got, want)
		}
	}
}

func firstValid(ps []position, lat bool) int32 {
	for _, p := range ps {
		if p.lat == lat && p.value != invalidSemicircle {
			return p.value
		}
	}
	return invalidSemicircle
}

func unscrubbedFields(files []*proto.FIT) string {
	var b strings.Builder
	for i, file := range files {
		fmt.Fprintf(&b, "file %d header %+v\n", i, file.FileHeader)
		for _, m := range file.Messages {
			if m.Num == mesgnum.UserProfile {
				fmt.Fprintf(&b, "%s scrubbed\n", m.Num)
				continue
			}
			for _, f := range m.Fields {
				value := fmt.Sprint(f.Value.Any())
				if f.Units == "semicircles" || f.Name == "serial_number" {
					value = "scrubbed"
				}
				fmt.Fprintf(&b, "%s.%d %s=%s\n", m.Num, f.Num, f.Name, value)
			}
			for _, d := range m.DeveloperFields {
				fmt.Fprintf(&b, "%s dev %d.%d=%v\n", m.Num, d.DeveloperDataIndex, d.Num, d.Value.Any())
			}
		}
	}
	return b.String()
}
