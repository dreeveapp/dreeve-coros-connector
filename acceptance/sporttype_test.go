package acceptance_test

import (
	"bufio"
	"encoding/json"
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

func jsonLines(t *testing.T, output string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		var line map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("line is not a JSON object: %v\n%s", err, scanner.Text())
		}
		lines = append(lines, line)
	}
	return lines
}

func filteredCount(t *testing.T, output string) float64 {
	t.Helper()
	for _, line := range jsonLines(t, output) {
		if msg, _ := line["msg"].(string); strings.Contains(msg, "finished") {
			n, ok := line["filtered"].(float64)
			if !ok {
				t.Fatalf("summary line has no filtered count: %v", line)
			}
			return n
		}
	}
	t.Fatalf("no summary line in:\n%s", output)
	return 0
}

func filteredLogged(t *testing.T, output string) map[string]float64 {
	t.Helper()
	logged := map[string]float64{}
	for _, line := range jsonLines(t, output) {
		if msg, _ := line["msg"].(string); !strings.Contains(msg, "filtered") {
			continue
		}
		id, _ := line["labelId"].(string)
		name, _ := line["name"].(string)
		sport, ok := line["sportType"].(float64)
		if id == "" || name == "" || !ok {
			t.Errorf("filtered activity line lacks labelId, name or sportType: %v", line)
			continue
		}
		logged[id] = sport
	}
	return logged
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
	if got, want := filteredLogged(t, r.stderr), map[string]float64{triathlonID: 10000, multiSportID: 10001}; !maps.Equal(got, want) {
		t.Errorf("filtered activities logged %v, want %v", got, want)
	}
	if n := filteredCount(t, r.stderr); n != 2 {
		t.Errorf("filtered count = %v, want 2", n)
	}
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
	w.addActivity(hyroxID, 1, withSportType(98))
	w.mustRun("login")
	w.env["ACTIVITY_TYPES"] = "customSport"

	w.mustRun("sync-once")

	files := w.watchFiles()
	if len(files) != 2 || files["coros_"+customID+".fit"] == nil || files["coros_"+hyroxID+".fit"] == nil {
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

	if got, want := filteredLogged(t, r.stderr), map[string]float64{triathlonID: 10000, multiSportID: 10001}; !maps.Equal(got, want) {
		t.Errorf("filtered activities logged %v, want %v", got, want)
	}
	if n := filteredCount(t, r.stderr); n != 2 {
		t.Errorf("filtered count = %v, want 2", n)
	}
	for _, line := range jsonLines(t, r.stderr) {
		if msg, _ := line["msg"].(string); strings.Contains(msg, "would deliver") && (line["labelId"] == triathlonID || line["labelId"] == multiSportID) {
			t.Errorf("dry run would deliver a filtered activity: %v", line)
		}
	}
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
