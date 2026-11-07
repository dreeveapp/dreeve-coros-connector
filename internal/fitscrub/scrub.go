package fitscrub

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/kit/hash/crc16"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
)

const SerialNumber uint32 = 1234567890

type Origin struct {
	Lat  float64
	Long float64
}

var (
	ErrNotFIT    = errors.New("not a FIT file")
	ErrTruncated = errors.New("truncated FIT file")
	ErrChecksum  = errors.New("FIT checksum mismatch")
	ErrMalformed = errors.New("malformed FIT file")
	ErrOrigin    = errors.New("origin out of range")
)

const invalidSemicircle uint32 = math.MaxInt32

type kind int

const (
	latitude kind = iota
	longitude
	serialNumber
	cleared
)

type value struct {
	kind     kind
	offset   int
	size     int
	order    binary.ByteOrder
	baseType byte
}

type file struct {
	start, end int
}

func Scrub(fit []byte, origin Origin) ([]byte, error) {
	if origin.Lat < -90 || origin.Lat > 90 || origin.Long < -180 || origin.Long >= 180 {
		return nil, fmt.Errorf("%w: %v", ErrOrigin, origin)
	}
	files, values, err := parse(fit)
	if err != nil {
		return nil, err
	}
	if err := decodable(fit); err != nil {
		return nil, err
	}
	out := bytes.Clone(fit)
	shiftLat, shiftLong := translation(out, values, origin)
	for _, v := range values {
		switch v.kind {
		case latitude:
			translate(out, v, shiftLat)
		case longitude:
			translate(out, v, shiftLong)
		case serialNumber:
			v.order.PutUint32(out[v.offset:], SerialNumber)
		case cleared:
			clear(out, v)
		}
	}
	for _, f := range files {
		binary.LittleEndian.PutUint16(out[f.end:], checksum(out[f.start:f.end]))
	}
	return out, nil
}

func decodable(fit []byte) error {
	dec := decoder.New(bytes.NewReader(fit), decoder.WithNoComponentExpansion(), decoder.WithBroadcastOnly())
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			return fmt.Errorf("%w: %w", ErrMalformed, err)
		}
	}
	return nil
}

func semicircles(degrees float64) uint32 {
	return uint32(int64(degrees*(1<<31)/180) & math.MaxUint32)
}

func translation(b []byte, values []value, origin Origin) (uint32, uint32) {
	var shiftLat, shiftLong uint32
	foundLat, foundLong := false, false
	for _, v := range values {
		if v.kind != latitude && v.kind != longitude {
			continue
		}
		position := v.order.Uint32(b[v.offset:])
		if position == invalidSemicircle {
			continue
		}
		if v.kind == latitude && !foundLat {
			shiftLat, foundLat = semicircles(origin.Lat)-position, true
		}
		if v.kind == longitude && !foundLong {
			shiftLong, foundLong = semicircles(origin.Long)-position, true
		}
	}
	return shiftLat, shiftLong
}

func translate(b []byte, v value, shift uint32) {
	position := v.order.Uint32(b[v.offset:])
	if position == invalidSemicircle {
		return
	}
	v.order.PutUint32(b[v.offset:], position+shift)
}

func clear(b []byte, v value) {
	field := b[v.offset : v.offset+v.size]
	switch v.baseType & 0x1F {
	case 0x07, 0x0A, 0x0B, 0x0C, 0x10:
		for i := range field {
			field[i] = 0x00
		}
		return
	}
	for i := range field {
		field[i] = 0xFF
	}
	signedSize := map[byte]int{0x01: 1, 0x03: 2, 0x05: 4, 0x0E: 8}[v.baseType&0x1F]
	if signedSize == 0 {
		return
	}
	for i := 0; i+signedSize <= len(field); i += signedSize {
		if v.order == binary.BigEndian {
			field[i] = 0x7F
		} else {
			field[i+signedSize-1] = 0x7F
		}
	}
}

func checksum(b []byte) uint16 {
	h := crc16.New()
	_, _ = h.Write(b)
	return h.Sum16()
}

type definition struct {
	size   int
	values []value
}

func parse(b []byte) ([]file, []value, error) {
	if len(b) == 0 {
		return nil, nil, ErrNotFIT
	}
	var files []file
	var values []value
	for start := 0; start < len(b); {
		f, vs, err := parseFile(b, start)
		if err != nil {
			return nil, nil, fmt.Errorf("file at byte %d: %w", start, err)
		}
		files = append(files, f)
		values = append(values, vs...)
		start = f.end + 2
	}
	return files, values, nil
}

func parseFile(b []byte, start int) (file, []value, error) {
	if len(b)-start < 12 {
		return file{}, nil, ErrNotFIT
	}
	headerSize := int(b[start])
	if (headerSize != 12 && headerSize != 14) || string(b[start+8:start+12]) != ".FIT" {
		return file{}, nil, ErrNotFIT
	}
	if len(b)-start < headerSize {
		return file{}, nil, ErrTruncated
	}
	if headerSize == 14 {
		stored := binary.LittleEndian.Uint16(b[start+12:])
		if stored != 0 && stored != checksum(b[start:start+12]) {
			return file{}, nil, fmt.Errorf("%w in header", ErrChecksum)
		}
	}
	dataSize := int(binary.LittleEndian.Uint32(b[start+4:]))
	end := start + headerSize + dataSize
	if dataSize < 0 || end+2 > len(b) || end < start {
		return file{}, nil, ErrTruncated
	}
	if binary.LittleEndian.Uint16(b[end:]) != checksum(b[start:end]) {
		return file{}, nil, ErrChecksum
	}
	values, err := parseRecords(b[:end], start+headerSize)
	if err != nil {
		return file{}, nil, err
	}
	return file{start: start, end: end}, values, nil
}

func parseRecords(b []byte, offset int) ([]value, error) {
	var definitions [16]*definition
	var values []value
	for offset < len(b) {
		header := b[offset]
		offset++
		switch {
		case header&0x80 != 0:
			d := definitions[(header>>5)&0x03]
			if d == nil {
				return nil, fmt.Errorf("%w: data message before its definition at byte %d", ErrMalformed, offset-1)
			}
			values = appendValues(values, d, offset)
			offset += d.size
		case header&0x40 != 0:
			d, size, err := parseDefinition(b, offset, header&0x20 != 0)
			if err != nil {
				return nil, err
			}
			definitions[header&0x0F] = d
			offset += size
		default:
			d := definitions[header&0x0F]
			if d == nil {
				return nil, fmt.Errorf("%w: data message before its definition at byte %d", ErrMalformed, offset-1)
			}
			values = appendValues(values, d, offset)
			offset += d.size
		}
		if offset > len(b) {
			return nil, fmt.Errorf("%w: record runs past the data size", ErrMalformed)
		}
	}
	return values, nil
}

func appendValues(values []value, d *definition, offset int) []value {
	for _, v := range d.values {
		v.offset += offset
		values = append(values, v)
	}
	return values
}

func parseDefinition(b []byte, offset int, developer bool) (*definition, int, error) {
	if offset+5 > len(b) {
		return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
	}
	var order binary.ByteOrder = binary.LittleEndian
	switch b[offset+1] {
	case 0:
	case 1:
		order = binary.BigEndian
	default:
		return nil, 0, fmt.Errorf("%w: unknown architecture %d", ErrMalformed, b[offset+1])
	}
	mesg := typedef.MesgNum(order.Uint16(b[offset+2:]))
	count := int(b[offset+4])
	cursor := offset + 5
	if cursor+3*count > len(b) {
		return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
	}
	d := &definition{}
	for i := 0; i < count; i++ {
		num, size, baseType := b[cursor], int(b[cursor+1]), b[cursor+2]
		cursor += 3
		if k, ok := classify(mesg, num, size, baseType); ok {
			d.values = append(d.values, value{kind: k, offset: d.size, size: size, order: order, baseType: baseType})
		}
		d.size += size
	}
	if developer {
		if cursor+1 > len(b) {
			return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
		}
		devCount := int(b[cursor])
		cursor++
		if cursor+3*devCount > len(b) {
			return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
		}
		for i := 0; i < devCount; i++ {
			d.size += int(b[cursor+1])
			cursor += 3
		}
	}
	return d, cursor - offset, nil
}

func classify(mesg typedef.MesgNum, num byte, size int, baseType byte) (kind, bool) {
	if mesg == mesgnum.UserProfile {
		return cleared, true
	}
	field := factory.CreateField(mesg, num)
	switch {
	case field.Units == "semicircles" && size == 4 && baseType&0x1F == 0x05:
		if strings.HasSuffix(field.Name, "_lat") {
			return latitude, true
		}
		if strings.HasSuffix(field.Name, "_long") {
			return longitude, true
		}
	case field.Name == "serial_number" && size == 4 && baseType&0x1F == 0x0C:
		return serialNumber, true
	}
	return 0, false
}
