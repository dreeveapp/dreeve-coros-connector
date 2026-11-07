package fittitle

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/muktihari/fit/kit/hash/crc16"
)

const (
	MaxTitleBytes = 254

	workoutMesgNum = 26
	wktNameField   = 8
	stringBaseType = 0x07
)

var (
	ErrHasWorkout = errors.New("FIT file already has a workout message")
	ErrEmptyTitle = errors.New("empty title")
	ErrNotFIT     = errors.New("not a FIT file")
	ErrTruncated  = errors.New("truncated FIT file")
	ErrChecksum   = errors.New("FIT checksum mismatch")
	ErrMalformed  = errors.New("malformed FIT file")
)

type file struct {
	start, headerSize, end int
}

func Write(fit []byte, title string) ([]byte, error) {
	name := truncate(sanitize(title))
	if strings.TrimSpace(name) == "" {
		return nil, ErrEmptyTitle
	}
	files, err := parse(fit)
	if err != nil {
		return nil, err
	}
	last := files[len(files)-1]

	workout := workoutRecords(name)
	dataSize := int(binary.LittleEndian.Uint32(fit[last.start+4:])) + len(workout)
	if dataSize < 0 || dataSize > math.MaxUint32 {
		return nil, fmt.Errorf("%w: no room for the title", ErrTruncated)
	}
	out := make([]byte, 0, len(fit)+len(workout))
	out = append(out, fit[:last.end]...)
	out = append(out, workout...)
	out = append(out, 0, 0)

	binary.LittleEndian.PutUint32(out[last.start+4:], uint32(dataSize))
	if last.headerSize == 14 {
		binary.LittleEndian.PutUint16(out[last.start+12:], checksum(out[last.start:last.start+12]))
	}
	end := last.end + len(workout)
	binary.LittleEndian.PutUint16(out[end:], checksum(out[last.start:end]))
	return out, nil
}

func sanitize(title string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(title, ""), "\x00", "")
}

func truncate(title string) string {
	if len(title) <= MaxTitleBytes {
		return title
	}
	cut := MaxTitleBytes
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut]
}

func workoutRecords(name string) []byte {
	size := min(len(name)+1, math.MaxUint8)
	records := []byte{0x40, 0, 0, workoutMesgNum, 0, 1, wktNameField, byte(size), stringBaseType, 0x00}
	records = append(records, name...)
	return append(records, 0)
}

func parse(b []byte) ([]file, error) {
	if len(b) == 0 {
		return nil, ErrNotFIT
	}
	var files []file
	for start := 0; start < len(b); {
		f, err := parseFile(b, start)
		if err != nil {
			return nil, fmt.Errorf("file at byte %d: %w", start, err)
		}
		files = append(files, f)
		start = f.end + 2
	}
	return files, nil
}

func parseFile(b []byte, start int) (file, error) {
	if len(b)-start < 12 {
		return file{}, ErrNotFIT
	}
	headerSize := int(b[start])
	if (headerSize != 12 && headerSize != 14) || string(b[start+8:start+12]) != ".FIT" {
		return file{}, ErrNotFIT
	}
	if len(b)-start < headerSize {
		return file{}, ErrTruncated
	}
	if headerSize == 14 {
		stored := binary.LittleEndian.Uint16(b[start+12:])
		if stored != 0 && stored != checksum(b[start:start+12]) {
			return file{}, fmt.Errorf("%w in header", ErrChecksum)
		}
	}
	dataSize := int(binary.LittleEndian.Uint32(b[start+4:]))
	end := start + headerSize + dataSize
	if end+2 > len(b) || end < start {
		return file{}, ErrTruncated
	}
	if binary.LittleEndian.Uint16(b[end:]) != checksum(b[start:end]) {
		return file{}, ErrChecksum
	}
	if err := walkRecords(b[:end], start+headerSize); err != nil {
		return file{}, err
	}
	return file{start: start, headerSize: headerSize, end: end}, nil
}

type definition struct {
	mesgNum uint16
	size    int
}

func walkRecords(b []byte, offset int) error {
	var definitions [16]*definition
	for offset < len(b) {
		header := b[offset]
		offset++
		var local byte
		switch {
		case header&0x80 != 0:
			local = (header >> 5) & 0x03
		case header&0x40 != 0:
			d, size, err := parseDefinition(b, offset, header&0x20 != 0)
			if err != nil {
				return err
			}
			definitions[header&0x0F] = d
			offset += size
			continue
		default:
			local = header & 0x0F
		}
		d := definitions[local]
		if d == nil {
			return fmt.Errorf("%w: data message before its definition at byte %d", ErrMalformed, offset-1)
		}
		if d.mesgNum == workoutMesgNum {
			return ErrHasWorkout
		}
		offset += d.size
		if offset > len(b) {
			return fmt.Errorf("%w: record runs past the data size", ErrMalformed)
		}
	}
	return nil
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
	d := &definition{mesgNum: order.Uint16(b[offset+2:])}
	count := int(b[offset+4])
	cursor := offset + 5
	if cursor+3*count > len(b) {
		return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
	}
	for i := 0; i < count; i++ {
		d.size += int(b[cursor+1])
		cursor += 3
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

func checksum(b []byte) uint16 {
	h := crc16.New()
	_, _ = h.Write(b)
	return h.Sum16()
}
