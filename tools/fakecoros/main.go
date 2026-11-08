package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

const (
	demoEmail    = "runner@example.com"
	demoPassword = "demo-password"
)

type runner struct {
	cleanups []func()
}

func (r *runner) Helper() {}

func (r *runner) Fatalf(format string, args ...any) {
	r.close()
	log.Fatalf(format, args...)
}

func (r *runner) Errorf(format string, args ...any) {
	log.Printf(format, args...)
}

func (r *runner) Cleanup(f func()) {
	r.cleanups = append(r.cleanups, f)
}

func (r *runner) close() {
	for _, cleanup := range slices.Backward(r.cleanups) {
		cleanup()
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	advertise := flag.String("advertise", "", "host name the connector uses to reach the fake, e.g. host.docker.internal")
	fixtures := flag.String("fixtures", "testdata/coros", "recorded COROS fixtures")
	region := flag.Int("region", 1, "region id reported at login")
	homeRegion := flag.Int("home-region", 0, "region whose host accepts the token, defaults to -region")
	activities := flag.Int("activities", 3, "activities to serve, one per day starting yesterday")
	withoutFile := flag.Int("without-file", 0, "how many of the oldest activities have no FIT file")
	throttle := flag.Int("throttle", 0, "how many of the first activity download requests answer HTTP 429")
	fitFile := flag.String("fit", "", "FIT file served for every activity, e.g. testdata/fit/coros-outdoor-run.fit; a small generated activity when empty")
	flag.Parse()

	var content []byte
	if *fitFile != "" {
		var err error
		if content, err = os.ReadFile(*fitFile); err != nil {
			log.Fatalf("read -fit: %v", err)
		}
	}

	r := &runner{}
	defer r.close()
	fake := fakecoros.New(r, fakecoros.Config{
		FixturesDir:    *fixtures,
		Email:          demoEmail,
		Password:       demoPassword,
		RegionID:       *region,
		HomeRegionID:   *homeRegion,
		ListenAddr:     *addr,
		AdvertisedHost: *advertise,
	})
	for i := range *activities {
		id := "9000000000000" + fmt.Sprintf("%05d", i+2)
		fake.AddActivity(fakecoros.Activity{
			LabelID:   id,
			SportType: 100,
			Name:      "Demo run " + strconv.Itoa(i+1),
			Start:     time.Now().UTC().AddDate(0, 0, -(i + 1)),
			Content:   content,
			NoFile:    i >= *activities-*withoutFile,
		})
	}

	for range *throttle {
		fake.InjectFaults(fakecoros.Download, fakecoros.Throttle)
	}

	fmt.Printf("COROS_API_HOSTS=%s\nCOROS_EMAIL=%s\nCOROS_PASSWORD=%s\n", fake.HostsEnv(), demoEmail, demoPassword)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
