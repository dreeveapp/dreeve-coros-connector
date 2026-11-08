package fitscrub

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/muktihari/fit/decoder"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/mesgnum"

	"github.com/xballoy/dreeve-coros-connector/internal/fit"
)

const SerialNumber uint32 = 1234567890

type Origin struct {
	Lat  float64
	Long float64
}

var ErrOrigin = errors.New("origin out of range")

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

func Scrub(b []byte, origin Origin) ([]byte, error) {
	if origin.Lat < -90 || origin.Lat > 90 || origin.Long < -180 || origin.Long >= 180 {
		return nil, fmt.Errorf("%w: %v", ErrOrigin, origin)
	}
	var values []value
	files, err := fit.Walk(b, collect(&values))
	if err != nil {
		return nil, err
	}
	if err := decodable(b); err != nil {
		return nil, err
	}
	out := bytes.Clone(b)
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
	fit.Reseal(out, files)
	return out, nil
}

func decodable(b []byte) error {
	dec := decoder.New(bytes.NewReader(b), decoder.WithNoComponentExpansion(), decoder.WithBroadcastOnly())
	for dec.Next() {
		if _, err := dec.Decode(); err != nil {
			return fmt.Errorf("%w: %w", fit.ErrMalformed, err)
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

func collect(values *[]value) func(fit.Message) error {
	return func(m fit.Message) error {
		for _, f := range m.Fields {
			if k, ok := classify(m.Num, f); ok {
				*values = append(*values, value{kind: k, offset: f.Offset, size: f.Size, order: f.Order, baseType: f.BaseType})
			}
		}
		return nil
	}
}

func classify(mesg typedef.MesgNum, f fit.Field) (kind, bool) {
	if mesg == mesgnum.UserProfile {
		return cleared, true
	}
	field := factory.CreateField(mesg, f.Num)
	switch {
	case field.Units == "semicircles" && f.Size == 4 && f.BaseType&0x1F == 0x05:
		if strings.HasSuffix(field.Name, "_lat") {
			return latitude, true
		}
		if strings.HasSuffix(field.Name, "_long") {
			return longitude, true
		}
	case field.Name == "serial_number" && f.Size == 4 && f.BaseType&0x1F == 0x0C:
		return serialNumber, true
	}
	return 0, false
}
