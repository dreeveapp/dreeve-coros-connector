package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xballoy/dreeve-coros-connector/internal/fitscrub"
)

func main() {
	originFlag := flag.String("origin", "", "latitude,longitude in degrees where the first position of the track is moved to")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: fitscrub -origin LAT,LONG IN.fit OUT.fit")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(2)
	}
	origin, err := parseOrigin(*originFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fitscrub:", err)
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), origin); err != nil {
		fmt.Fprintln(os.Stderr, "fitscrub:", err)
		os.Exit(1)
	}
}

func parseOrigin(s string) (fitscrub.Origin, error) {
	lat, long, ok := strings.Cut(s, ",")
	if !ok {
		return fitscrub.Origin{}, fmt.Errorf("-origin must be LAT,LONG, got %q", s)
	}
	la, err := strconv.ParseFloat(strings.TrimSpace(lat), 64)
	if err != nil {
		return fitscrub.Origin{}, fmt.Errorf("-origin latitude: %w", err)
	}
	lo, err := strconv.ParseFloat(strings.TrimSpace(long), 64)
	if err != nil {
		return fitscrub.Origin{}, fmt.Errorf("-origin longitude: %w", err)
	}
	return fitscrub.Origin{Lat: la, Long: lo}, nil
}

func run(in, out string, origin fitscrub.Origin) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	scrubbed, err := fitscrub.Scrub(raw, origin)
	if err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	return os.WriteFile(out, scrubbed, 0o600)
}
