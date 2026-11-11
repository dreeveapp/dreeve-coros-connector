package cli

import (
	"fmt"
	"strconv"
	"strings"
)

type codeRange struct{ from, to int }

func code(c int) []codeRange { return []codeRange{{c, c}} }

var sportTypes = map[string][]codeRange{
	"run":              code(100),
	"indoorRun":        code(101),
	"trailRun":         code(102),
	"trackRun":         code(103),
	"hike":             code(104),
	"mtnClimb":         code(105),
	"climb":            code(106),
	"bike":             code(200),
	"indoorBike":       code(201),
	"roadEbike":        code(202),
	"gravelRoadBike":   code(203),
	"mountainRiding":   code(204),
	"mountainEbike":    code(205),
	"helmetBike":       code(299),
	"poolSwim":         code(300),
	"openWater":        code(301),
	"gymCardio":        code(400),
	"gpsCardio":        code(401),
	"strength":         code(402),
	"ski":              code(500),
	"snowboard":        code(501),
	"xcSki":            code(502),
	"skiTouring":       code(503),
	"row":              code(700),
	"indoorRow":        code(701),
	"whitewater":       code(702),
	"flatwater":        code(704),
	"windsurfing":      code(705),
	"speedsurfing":     code(706),
	"boatFishingLure":  code(707),
	"shoreFishingLure": code(708),
	"pondFishingLure":  code(709),
	"kayakFishingLure": code(710),
	"inshoreFishing":   code(711),
	"offshoreFishing":  code(712),
	"boatFlyFishing":   code(713),
	"shoreFlyFishing":  code(714),
	"surfFishing":      code(715),
	"indoorClimb":      code(800),
	"bouldering":       code(801),
	"outdoorClimb":     code(802),
	"walk":             code(900),
	"jumpRope":         code(901),
	"climbStairs":      code(902),
	"elliptical":       code(903),
	"yoga":             code(904),
	"pilates":          code(905),
	"boxing":           code(906),
	"badminton":        code(1000),
	"tableTennis":      code(1001),
	"basketball":       code(1002),
	"football":         code(1003),
	"pickleball":       code(1004),
	"tennis":           code(1005),
	"padel":            code(1006),
	"frisbee":          code(1100),
	"skateboarding":    code(1101),
	"hyrox":            code(1200),
	"triathlon":        code(10000),
	"multiSport":       code(10001),
	"skiTouringOld":    code(10002),
	"multiPitch":       code(10003),
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
				return sportTypeFilter{}, fmt.Errorf("EXCLUDE_ACTIVITY_TYPES entry %q is also in ACTIVITY_TYPES as %q; list each sport type in only one of them", x.raw, a.raw)
			}
		}
	}
	return sportTypeFilter{allow: allow, exclude: exclude}, nil
}

func sportTypeList(getenv Env, key string) ([]sportTypeEntry, error) {
	raw := getenv(key)
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var entries []sportTypeEntry
	for item := range strings.SplitSeq(raw, ",") {
		entry, err := parseSportType(strings.TrimSpace(item))
		if err != nil {
			return nil, fmt.Errorf("%s: %w, in %q", key, err, raw)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseSportType(item string) (sportTypeEntry, error) {
	if item == "" {
		return sportTypeEntry{}, fmt.Errorf("empty sport type")
	}
	if n, err := strconv.Atoi(item); err == nil || strings.TrimLeft(item, "+-0123456789") == "" {
		if err != nil || n < 1 {
			return sportTypeEntry{}, fmt.Errorf("sport type code %q must be a whole number of at least 1", item)
		}
		for name, ranges := range sportTypes {
			if len(ranges) == 1 && ranges[0].from == n && ranges[0].to == n {
				return sportTypeEntry{raw: item, key: name, ranges: ranges}, nil
			}
		}
		return sportTypeEntry{raw: item, key: item, ranges: code(n)}, nil
	}
	for name, ranges := range sportTypes {
		if strings.EqualFold(name, item) {
			return sportTypeEntry{raw: item, key: name, ranges: ranges}, nil
		}
	}
	return sportTypeEntry{}, fmt.Errorf("unknown sport type %q, expected a name from the sport type table or a numeric COROS sportType code", item)
}
