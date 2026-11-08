package fit

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/muktihari/fit/kit/hash/crc16"
	"github.com/muktihari/fit/profile/typedef"
)

var (
	ErrNotFIT    = errors.New("not a FIT file")
	ErrTruncated = errors.New("truncated FIT file")
	ErrChecksum  = errors.New("FIT checksum mismatch")
	ErrMalformed = errors.New("malformed FIT file")
)

type Field struct {
	Num      byte
	Offset   int
	Size     int
	BaseType byte
	Order    binary.ByteOrder
}

type Message struct {
	Num    typedef.MesgNum
	Fields []Field
}

type File struct {
	Start      int
	HeaderSize int
	End        int
}

func Walk(b []byte, visit func(Message) error) ([]File, error) {
	if len(b) == 0 {
		return nil, ErrNotFIT
	}
	var files []File
	for start := 0; start < len(b); {
		f, err := walkFile(b, start, visit)
		if err != nil {
			return nil, fmt.Errorf("file at byte %d: %w", start, err)
		}
		files = append(files, f)
		start = f.End + 2
	}
	return files, nil
}

func Reseal(b []byte, files []File) {
	for _, f := range files {
		if f.HeaderSize == 14 && binary.LittleEndian.Uint16(b[f.Start+12:]) != 0 {
			binary.LittleEndian.PutUint16(b[f.Start+12:], checksum(b[f.Start:f.Start+12]))
		}
		binary.LittleEndian.PutUint16(b[f.End:], checksum(b[f.checksummed():f.End]))
	}
}

func Append(b []byte, f File, records []byte) ([]byte, error) {
	dataSize := f.End - f.Start - f.HeaderSize + len(records)
	if dataSize < 0 || dataSize > math.MaxUint32 {
		return nil, fmt.Errorf("%w: no room for %d more bytes in the data size", ErrMalformed, len(records))
	}
	out := make([]byte, 0, len(b)+len(records))
	out = append(out, b[:f.End]...)
	out = append(out, records...)
	out = append(out, 0, 0)
	out = append(out, b[f.End+2:]...)
	binary.LittleEndian.PutUint32(out[f.Start+4:], uint32(dataSize))
	f.End += len(records)
	Reseal(out, []File{f})
	return out, nil
}

func (f File) checksummed() int {
	if f.HeaderSize == 14 {
		return f.Start + f.HeaderSize
	}
	return f.Start
}

func walkFile(b []byte, start int, visit func(Message) error) (File, error) {
	if len(b)-start < 12 {
		return File{}, ErrNotFIT
	}
	headerSize := int(b[start])
	if (headerSize != 12 && headerSize != 14) || string(b[start+8:start+12]) != ".FIT" {
		return File{}, ErrNotFIT
	}
	if len(b)-start < headerSize {
		return File{}, ErrTruncated
	}
	if headerSize == 14 {
		stored := binary.LittleEndian.Uint16(b[start+12:])
		if stored != 0 && stored != checksum(b[start:start+12]) {
			return File{}, fmt.Errorf("%w in header", ErrChecksum)
		}
	}
	end := start + headerSize + int(binary.LittleEndian.Uint32(b[start+4:]))
	if end < start || end+2 > len(b) {
		return File{}, ErrTruncated
	}
	f := File{Start: start, HeaderSize: headerSize, End: end}
	if binary.LittleEndian.Uint16(b[end:]) != checksum(b[f.checksummed():end]) {
		return File{}, ErrChecksum
	}
	if err := walkRecords(b[:end], start+headerSize, visit); err != nil {
		return File{}, err
	}
	return f, nil
}

type definition struct {
	num    typedef.MesgNum
	size   int
	fields []Field
}

func walkRecords(b []byte, offset int, visit func(Message) error) error {
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
		if offset+d.size > len(b) {
			return fmt.Errorf("%w: record runs past the data size", ErrMalformed)
		}
		if err := visit(d.message(offset)); err != nil {
			return err
		}
		offset += d.size
	}
	return nil
}

func (d *definition) message(offset int) Message {
	m := Message{Num: d.num, Fields: make([]Field, len(d.fields))}
	for i, f := range d.fields {
		f.Offset += offset
		m.Fields[i] = f
	}
	return m
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
	d := &definition{num: typedef.MesgNum(order.Uint16(b[offset+2:]))}
	count := int(b[offset+4])
	cursor := offset + 5
	if cursor+3*count > len(b) {
		return nil, 0, fmt.Errorf("%w: definition runs past the data size", ErrMalformed)
	}
	for range count {
		size := int(b[cursor+1])
		d.fields = append(d.fields, Field{Num: b[cursor], Offset: d.size, Size: size, BaseType: b[cursor+2], Order: order})
		d.size += size
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
		for range devCount {
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
