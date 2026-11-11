package cli

import (
	"fmt"
	"strconv"
	"strings"
)

type codeRange struct{ from, to int }

func exactly(c int) []codeRange { return []codeRange{{c, c}} }

var sportTypes = map[string][]codeRange{
	"run":              exactly(100),
	"indoorRun":        exactly(101),
	"trailRun":         exactly(102),
	"trackRun":         exactly(103),
	"hike":             exactly(104),
	"mtnClimb":         exactly(105),
	"climb":            exactly(106),
	"bike":             exactly(200),
	"indoorBike":       exactly(201),
	"roadEbike":        exactly(202),
	"gravelRoadBike":   exactly(203),
	"mountainRiding":   exactly(204),
	"mountainEbike":    exactly(205),
	"helmetBike":       exactly(299),
	"poolSwim":         exactly(300),
	"openWater":        exactly(301),
	"gymCardio":        exactly(400),
	"gpsCardio":        exactly(401),
	"strength":         exactly(402),
	"ski":              exactly(500),
	"snowboard":        exactly(501),
	"xcSki":            exactly(502),
	"skiTouring":       exactly(503),
	"row":              exactly(700),
	"indoorRow":        exactly(701),
	"whitewater":       exactly(702),
	"flatwater":        exactly(704),
	"windsurfing":      exactly(705),
	"speedsurfing":     exactly(706),
	"boatFishingLure":  exactly(707),
	"shoreFishingLure": exactly(708),
	"pondFishingLure":  exactly(709),
	"kayakFishingLure": exactly(710),
	"inshoreFishing":   exactly(711),
	"offshoreFishing":  exactly(712),
	"boatFlyFishing":   exactly(713),
	"shoreFlyFishing":  exactly(714),
	"surfFishing":      exactly(715),
	"indoorClimb":      exactly(800),
	"bouldering":       exactly(801),
	"outdoorClimb":     exactly(802),
	"walk":             exactly(900),
	"jumpRope":         exactly(901),
	"climbStairs":      exactly(902),
	"elliptical":       exactly(903),
	"yoga":             exactly(904),
	"pilates":          exactly(905),
	"boxing":           exactly(906),
	"badminton":        exactly(1000),
	"tableTennis":      exactly(1001),
	"basketball":       exactly(1002),
	"football":         exactly(1003),
	"pickleball":       exactly(1004),
	"tennis":           exactly(1005),
	"padel":            exactly(1006),
	"frisbee":          exactly(1100),
	"skateboarding":    exactly(1101),
	"hyrox":            exactly(1200),
	"triathlon":        exactly(10000),
	"multiSport":       exactly(10001),
	"skiTouringOld":    exactly(10002),
	"multiPitch":       exactly(10003),
	"customSport":      {{98, 98}, {9800, 9930}},
}

type sportTypeEntry struct {
	raw    string
	key    string
	ranges []codeRange
}

func (e sportTypeEntry) matches(sportType int) bool {
	for _, r := range e.ranges {
		if sportType >= r.from && sportType <= r.to {
			return true
		}
	}
	return false
}

type sportTypeFilter struct {
	allow   []sportTypeEntry
	exclude []sportTypeEntry
}

func (f sportTypeFilter) syncs(sportType int) bool {
	return (len(f.allow) == 0 || anyMatches(f.allow, sportType)) && !anyMatches(f.exclude, sportType)
}

func anyMatches(entries []sportTypeEntry, sportType int) bool {
	for _, e := range entries {
		if e.matches(sportType) {
			return true
		}
	}
	return false
}

func loadSportTypeFilter(getenv Env) (sportTypeFilter, error) {
	allow, err := sportTypeList(getenv, "ACTIVITY_TYPES")
	if err != nil {
		return sportTypeFilter{}, err
	}
	exclude, err := sportTypeList(getenv, "EXCLUDE_ACTIVITY_TYPES")
	if err != nil {
		return sportTypeFilter{}, err
	}
	for _, x := range exclude {
		for _, a := range allow {
			if x.key == a.key {
				return sportTypeFilter{}, fmt.Errorf("EXCLUDE_ACTIVITY_TYPES must not list a sport type that ACTIVITY_TYPES lists, got %q, listed in ACTIVITY_TYPES as %q", x.raw, a.raw)
			}
		}
	}
	return sportTypeFilter{allow: allow, exclude: exclude}, nil
}

var sportTypeNamesByCode = func() map[int]string {
	names := map[int]string{}
	for name, ranges := range sportTypes {
		if len(ranges) == 1 && ranges[0].from == ranges[0].to {
			names[ranges[0].from] = name
		}
	}
	return names
}()

func sportTypeList(getenv Env, key string) ([]sportTypeEntry, error) {
	raw := getenv(key)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var entries []sportTypeEntry
	for item := range strings.SplitSeq(raw, ",") {
		entry, ok := parseSportType(strings.TrimSpace(item))
		if !ok {
			return nil, fmt.Errorf("%s must list sport type names or COROS sportType codes of at least 1, separated by commas, got %q in %q", key, strings.TrimSpace(item), raw)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseSportType(item string) (sportTypeEntry, bool) {
	if n, err := strconv.Atoi(item); err == nil {
		if n < 1 {
			return sportTypeEntry{}, false
		}
		if name, ok := sportTypeNamesByCode[n]; ok {
			return sportTypeEntry{raw: item, key: name, ranges: sportTypes[name]}, true
		}
		return sportTypeEntry{raw: item, key: item, ranges: exactly(n)}, true
	}
	for name, ranges := range sportTypes {
		if strings.EqualFold(name, item) {
			return sportTypeEntry{raw: item, key: name, ranges: ranges}, true
		}
	}
	return sportTypeEntry{}, false
}
