package acceptance_test

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

const (
	runID        = "900000000000000011"
	trailRunID   = "900000000000000012"
	triathlonID  = "900000000000000013"
	multiSportID = "900000000000000014"
	customID     = "900000000000000015"
	hyroxID      = "900000000000000016"
	legacyCustom = "900000000000000017"
)

func withSportType(code int) func(*fakecoros.Activity) {
	return func(a *fakecoros.Activity) { a.SportType = code }
}

func (w *world) addMixedSports() {
	w.addActivity(runID, 6, withSportType(100))
	w.addActivity(trailRunID, 5, withSportType(102))
	w.addActivity(triathlonID, 4, withSportType(10000))
	w.addActivity(multiSportID, 3, withSportType(10001))
	w.addActivity(customID, 2, withSportType(9850))
	w.addActivity(hyroxID, 1, withSportType(1200))
}

var mixedSportIDs = []string{runID, trailRunID, triathlonID, multiSportID, customID, hyroxID}

func (w *world) assertDeliveredExactly(ids ...string) {
	w.t.Helper()
	files := w.watchFiles()
	l := w.ledger()
	for _, id := range mixedSportIDs {
		want := slices.Contains(ids, id)
		_, inWatch := files["coros_"+id+".fit"]
		_, inLedger := l.Activities[id]
		if inWatch != want || inLedger != want {
			w.t.Errorf("activity %s: in watch folder %v, in ledger %v, want %v", id, inWatch, inLedger, want)
		}
		if downloads := w.fake.RequestsTo(fakecoros.Download, id); !want && downloads != 0 {
			w.t.Errorf("filtered activity %s was downloaded %d times", id, downloads)
		}
	}
}

func (w *world) assertFiltered(output string, want map[string]int) {
	w.t.Helper()
	logged := map[string]int{}
	summaries := 0
	for _, r := range jsonRecords(w.t, "sync-once", output) {
		switch {
		case *r.Msg == "filtered out by sport type":
			if r.LabelID == "" || r.Name == "" || r.SportType == nil {
				w.t.Errorf("filtered activity line lacks labelId, name or sportType: %+v", r)
				continue
			}
			logged[r.LabelID] = *r.SportType
		case strings.HasSuffix(*r.Msg, "finished") || strings.HasSuffix(*r.Msg, "finished, nothing written"):
			summaries++
			if r.Filtered == nil || *r.Filtered != len(want) {
				w.t.Errorf("summary line filtered count = %v, want %d", r.Filtered, len(want))
			}
		case strings.HasPrefix(*r.Msg, "dry run: would deliver") && want[r.LabelID] != 0:
			w.t.Errorf("dry run would deliver filtered activity %s", r.LabelID)
		}
	}
	if !maps.Equal(logged, want) {
		w.t.Errorf("filtered activities logged %v, want %v", logged, want)
	}
	if summaries != 1 {
		w.t.Errorf("expected one summary line, got %d", summaries)
	}
}

func TestEverySportTypeIsDeliveredByDefault(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")

	w.mustRun("sync-once")

	w.assertDeliveredExactly(mixedSportIDs...)
}

func TestExcludedSportTypesAreNeitherDownloadedNorRecorded(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "triathlon,multiSport"
	w.env["LOG_FORMAT"] = "json"

	r := w.mustRun("sync-once")

	w.assertDeliveredExactly(runID, trailRunID, customID, hyroxID)
	w.assertFiltered(r.stderr, map[string]int{triathlonID: 10000, multiSportID: 10001})
}

func TestOnlyAllowedSportTypesAreDelivered(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")
	w.env["ACTIVITY_TYPES"] = "run,trailRun"

	w.mustRun("sync-once")

	w.assertDeliveredExactly(runID, trailRunID)
}

func TestSportTypesCombineAllowAndExcludeLists(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")
	w.env["ACTIVITY_TYPES"] = "run, trailRun, hyrox"
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "customSport"

	w.mustRun("sync-once")

	w.assertDeliveredExactly(runID, trailRunID, hyroxID)
}

func TestSportTypeEntriesMatchByCodeOrCaseInsensitiveName(t *testing.T) {
	for _, value := range []string{"1200", "HYROX", "hyrox"} {
		t.Run(value, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.addMixedSports()
			w.mustRun("login")
			w.env["EXCLUDE_ACTIVITY_TYPES"] = value

			w.mustRun("sync-once")

			w.assertDeliveredExactly(runID, trailRunID, triathlonID, multiSportID, customID)
		})
	}
}

func TestCustomSportMatchesEveryUserDefinedSport(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(runID, 3, withSportType(100))
	w.addActivity(customID, 2, withSportType(9850))
	w.addActivity(legacyCustom, 1, withSportType(98))
	w.mustRun("login")
	w.env["ACTIVITY_TYPES"] = "customSport"

	w.mustRun("sync-once")

	files := w.watchFiles()
	if len(files) != 2 || files["coros_"+customID+".fit"] == nil || files["coros_"+legacyCustom+".fit"] == nil {
		t.Fatalf("expected only the custom sports, got %v", keys(files))
	}
}

func TestFilteredSportTypeIsDeliveredOnceAllowedAgain(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "triathlon,multiSport"
	w.mustRun("sync-once")

	delete(w.env, "EXCLUDE_ACTIVITY_TYPES")
	w.mustRun("sync-once")

	w.assertDeliveredExactly(mixedSportIDs...)
}

func TestFilteredActivityDoesNotKeepTheLookbackWindowOpen(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(triathlonID, 15, withSportType(10000))
	w.mustRun("login")
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "triathlon"
	w.mustRun("sync-once")

	delete(w.env, "EXCLUDE_ACTIVITY_TYPES")
	w.mustRun("sync-once")

	if n := w.fake.RequestsTo(fakecoros.Download, triathlonID); n != 0 {
		t.Fatalf("activity outside the lookback window was downloaded %d times", n)
	}
}

func TestFilteredActivitiesDoNotUseTheDownloadBudget(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addActivity(triathlonID, 3, withSportType(10000))
	w.addActivity(multiSportID, 2, withSportType(10001))
	w.addActivity(runID, 1, withSportType(100))
	w.mustRun("login")
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "triathlon,multiSport"
	w.env["MAX_DOWNLOADS_PER_CYCLE"] = "1"

	w.mustRun("sync-once")

	if files := w.watchFiles(); len(files) != 1 || files["coros_"+runID+".fit"] == nil {
		t.Fatalf("expected the run to be delivered in the first cycle, got %v", keys(files))
	}
}

func TestDryRunReportsFilteredActivities(t *testing.T) {
	w := newWorld(t, 1, 1)
	w.addMixedSports()
	w.mustRun("login")
	w.env["EXCLUDE_ACTIVITY_TYPES"] = "triathlon,multiSport"
	w.env["DRY_RUN"] = "true"
	w.env["LOG_FORMAT"] = "json"

	r := w.mustRun("sync-once")

	w.assertFiltered(r.stderr, map[string]int{triathlonID: 10000, multiSportID: 10001})
}

func TestInvalidSportTypeSettingsFailAtStartup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     map[string]string
		mention []string
	}{
		{"unknown name", map[string]string{"ACTIVITY_TYPES": "run,parkour"}, []string{"ACTIVITY_TYPES", "parkour"}},
		{"query value all", map[string]string{"EXCLUDE_ACTIVITY_TYPES": "all"}, []string{"EXCLUDE_ACTIVITY_TYPES", "all"}},
		{"zero code", map[string]string{"EXCLUDE_ACTIVITY_TYPES": "0"}, []string{"EXCLUDE_ACTIVITY_TYPES", "0"}},
		{"negative code", map[string]string{"ACTIVITY_TYPES": "-100"}, []string{"ACTIVITY_TYPES", "-100"}},
		{"empty entry", map[string]string{"ACTIVITY_TYPES": "run,,trailRun"}, []string{"ACTIVITY_TYPES"}},
		{"in both lists", map[string]string{"ACTIVITY_TYPES": "run,trailRun", "EXCLUDE_ACTIVITY_TYPES": "trailRun"}, []string{"ACTIVITY_TYPES", "EXCLUDE_ACTIVITY_TYPES", "trailRun"}},
		{"in both lists by code", map[string]string{"ACTIVITY_TYPES": "run", "EXCLUDE_ACTIVITY_TYPES": "100"}, []string{"ACTIVITY_TYPES", "EXCLUDE_ACTIVITY_TYPES", "100"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld(t, 1, 1)
			w.mustRun("login")
			queriesBefore := len(w.requestsTo(fakecoros.Query))
			maps.Copy(w.env, tc.env)

			r := w.run("sync-once")

			if r.code == 0 {
				t.Fatal("expected a non-zero exit")
			}
			assertContains(t, r.stderr, tc.mention...)
			if n := len(w.requestsTo(fakecoros.Query)) - queriesBefore; n != 0 {
				t.Errorf("queried COROS %d times despite the invalid configuration", n)
			}
		})
	}
}
