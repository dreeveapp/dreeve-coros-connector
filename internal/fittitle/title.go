package fittitle

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"

	"github.com/xballoy/dreeve-coros-connector/internal/fit"
)

const (
	MaxTitleBytes = 254

	stringBaseType = 0x07
)

var (
	ErrHasWorkout = errors.New("FIT file already has a workout message")
	ErrEmptyTitle = errors.New("empty title")
)

func Write(b []byte, title string) ([]byte, error) {
	name := truncate(strings.TrimSpace(sanitize(title)))
	if name == "" {
		return nil, ErrEmptyTitle
	}
	files, err := fit.Walk(b, rejectWorkout)
	if err != nil {
		return nil, err
	}
	return fit.Append(b, files[len(files)-1], workoutRecords(name))
}

func rejectWorkout(m fit.Message) error {
	if m.Num == mesgnum.Workout {
		return ErrHasWorkout
	}
	return nil
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
	records := []byte{0x40, 0, 0, byte(mesgnum.Workout), byte(mesgnum.Workout >> 8), 1, fieldnum.WorkoutWktName, byte(size), stringBaseType, 0x00}
	records = append(records, name...)
	return append(records, 0)
}
