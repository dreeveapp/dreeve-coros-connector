package fakecoros

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/muktihari/fit/encoder"
	"github.com/muktihari/fit/profile/factory"
	"github.com/muktihari/fit/profile/typedef"
	"github.com/muktihari/fit/profile/untyped/fieldnum"
	"github.com/muktihari/fit/profile/untyped/mesgnum"
	"github.com/muktihari/fit/proto"
)

const (
	fixtureFileHost = "http://coros-files.test/"
	fitEpoch        = 631065600
)

type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Errorf(format string, args ...any)
	Cleanup(func())
}

type Config struct {
	FixturesDir    string
	Email          string
	Password       string
	RegionID       int
	HomeRegionID   int
	ListenAddr     string
	AdvertisedHost string
}

type Activity struct {
	LabelID   string
	SportType int
	Name      string
	Start     time.Time
	NoFile    bool
	Content   []byte
}

type Endpoint string

const (
	Login    Endpoint = "login"
	Query    Endpoint = "activity query"
	Download Endpoint = "activity download"
	File     Endpoint = "file download"
)

type Fault int

const (
	Throttle Fault = iota + 1
	ServerError
	Truncate
	Stall
	ClientError
)

type Request struct {
	Endpoint Endpoint
	Region   int
	LabelID  string
	At       time.Time
}

type Server struct {
	t        TB
	cfg      Config
	srv      *httptest.Server
	base     string
	fixtures map[string]map[string]any

	mu         sync.Mutex
	tokens     map[string]bool
	activities map[string]Activity
	tokenSeq   int
	fileGets   map[string]int
	faults     map[Endpoint][]Fault
	requests   []Request
}

func New(t TB, cfg Config) *Server {
	t.Helper()
	if cfg.HomeRegionID == 0 {
		cfg.HomeRegionID = cfg.RegionID
	}
	s := &Server{
		t:          t,
		cfg:        cfg,
		fixtures:   map[string]map[string]any{},
		tokens:     map[string]bool{},
		activities: map[string]Activity{},
		fileGets:   map[string]int{},
		faults:     map[Endpoint][]Fault{},
	}
	for _, name := range []string{"login", "login-bad-credentials", "query-token-revoked", "query-wrong-region", "query-activities-page-1", "download-fit", "download-no-file.synthetic", "http-error.synthetic"} {
		s.fixtures[name] = loadFixture(t, cfg.FixturesDir, name)
	}

	s.srv = httptest.NewUnstartedServer(http.HandlerFunc(s.serve))
	if cfg.ListenAddr != "" {
		l, err := net.Listen("tcp", cfg.ListenAddr)
		if err != nil {
			t.Fatalf("fakecoros: listen %s: %v", cfg.ListenAddr, err)
		}
		_ = s.srv.Listener.Close()
		s.srv.Listener = l
	}
	s.srv.Start()
	t.Cleanup(s.srv.Close)

	s.base = s.srv.URL
	if cfg.AdvertisedHost != "" {
		_, port, _ := net.SplitHostPort(s.srv.Listener.Addr().String())
		s.base = "http://" + net.JoinHostPort(cfg.AdvertisedHost, port)
	}
	return s
}

func loadFixture(t TB, dir, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		t.Fatalf("fakecoros: read fixture %s: %v", name, err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("fakecoros: decode fixture %s: %v", name, err)
	}
	return v
}

func (s *Server) RegionURL(regionID int) string {
	return fmt.Sprintf("%s/r%d", s.base, regionID)
}

func (s *Server) HostsEnv() string {
	parts := make([]string, 0, 3)
	for _, id := range []int{1, 2, 3} {
		parts = append(parts, fmt.Sprintf("%d=%s", id, s.RegionURL(id)))
	}
	return strings.Join(parts, ",")
}

func (s *Server) AddActivity(a Activity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.Content == nil && !a.NoFile {
		a.Content = s.activityFIT(a)
	}
	s.activities[a.LabelID] = a
}

func (s *Server) activityFIT(a Activity) []byte {
	field := factory.CreateField
	created := uint32(a.Start.Unix() - fitEpoch)
	file := &proto.FIT{Messages: []proto.Message{
		{Num: mesgnum.FileId, Fields: []proto.Field{
			field(mesgnum.FileId, fieldnum.FileIdType).WithValue(typedef.FileActivity.Byte()),
			field(mesgnum.FileId, fieldnum.FileIdManufacturer).WithValue(typedef.ManufacturerCoros.Uint16()),
			field(mesgnum.FileId, fieldnum.FileIdTimeCreated).WithValue(created),
		}},
		{Num: mesgnum.Record, Fields: []proto.Field{
			field(mesgnum.Record, fieldnum.RecordTimestamp).WithValue(created),
			field(mesgnum.Record, fieldnum.RecordHeartRate).WithValue(uint8(120)),
		}},
	}}
	var buf strings.Builder
	if err := encoder.New(&buf).Encode(file); err != nil {
		s.t.Errorf("fakecoros: encode the FIT file of %s: %v", a.LabelID, err)
	}
	return []byte(buf.String())
}

func (s *Server) RevokeTokens() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.tokens)
}

func (s *Server) FileDownloads(labelID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileGets[labelID]
}

func (s *Server) InjectFaults(e Endpoint, faults ...Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults[e] = append(s.faults[e], faults...)
}

func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

func (s *Server) RequestsTo(e Endpoint, labelID string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Endpoint == e && r.LabelID == labelID {
			n++
		}
	}
	return n
}

func (s *Server) RegionsOf(e Endpoint) []int {
	var regions []int
	for _, r := range s.Requests() {
		if r.Endpoint == e {
			regions = append(regions, r.Region)
		}
	}
	return regions
}

func (s *Server) receive(e Endpoint, region int, labelID string) Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Endpoint: e, Region: region, LabelID: labelID, At: time.Now()})
	queue := s.faults[e]
	if len(queue) == 0 {
		return 0
	}
	s.faults[e] = queue[1:]
	return queue[0]
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, fault Fault) bool {
	switch fault {
	case Throttle:
		s.writeJSONStatus(w, http.StatusTooManyRequests, s.fixtures["http-error.synthetic"])
	case ServerError:
		s.writeJSONStatus(w, http.StatusServiceUnavailable, s.fixtures["http-error.synthetic"])
	case ClientError:
		s.writeJSONStatus(w, http.StatusBadRequest, s.fixtures["http-error.synthetic"])
	case Stall:
		<-r.Context().Done()
	default:
		return false
	}
	return true
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if name, ok := strings.CutPrefix(r.URL.Path, "/files/"); ok {
		s.serveFile(w, r, strings.TrimSuffix(name, ".fit"))
		return
	}
	region, path, ok := s.splitRegion(r.URL.Path)
	if !ok {
		http.Error(w, "unknown path", http.StatusNotFound)
		return
	}
	switch {
	case r.Method == http.MethodPost && path == "/account/login":
		if !s.fail(w, r, s.receive(Login, region, "")) {
			s.serveLogin(w, r)
		}
	case r.Method == http.MethodGet && path == "/activity/query":
		if !s.fail(w, r, s.receive(Query, region, "")) {
			s.withToken(w, r, region, s.serveQuery)
		}
	case r.Method == http.MethodPost && path == "/activity/detail/download":
		if !s.fail(w, r, s.receive(Download, region, r.URL.Query().Get("labelId"))) {
			s.withToken(w, r, region, s.serveDownload)
		}
	default:
		http.Error(w, "unexpected request "+r.Method+" "+path, http.StatusNotFound)
	}
}

func (s *Server) splitRegion(path string) (int, string, bool) {
	rest, ok := strings.CutPrefix(path, "/r")
	if !ok || len(rest) < 2 {
		return 0, "", false
	}
	region, err := strconv.Atoi(rest[:1])
	if err != nil {
		return 0, "", false
	}
	return region, rest[1:], true
}

func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Account     string `json:"account"`
		AccountType int    `json:"accountType"`
		Pwd         string `json:"pwd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AccountType != 2 {
		http.Error(w, "malformed login body", http.StatusBadRequest)
		return
	}
	sum := md5.Sum([]byte(s.cfg.Password))
	if body.Account != s.cfg.Email || body.Pwd != hex.EncodeToString(sum[:]) {
		s.writeJSON(w, s.fixtures["login-bad-credentials"])
		return
	}

	s.mu.Lock()
	s.tokenSeq++
	token := fmt.Sprintf("fake-token-%d", s.tokenSeq)
	clear(s.tokens)
	s.tokens[token] = true
	s.mu.Unlock()

	resp := cloneJSON(s.fixtures["login"])
	data := resp["data"].(map[string]any)
	data["accessToken"] = token
	data["regionId"] = s.cfg.RegionID
	data["email"] = s.cfg.Email
	s.writeJSON(w, resp)
}

func (s *Server) withToken(w http.ResponseWriter, r *http.Request, region int, next http.HandlerFunc) {
	token := r.Header.Get("accessToken")
	s.mu.Lock()
	valid := s.tokens[token]
	s.mu.Unlock()
	if !valid {
		s.writeJSON(w, s.fixtures["query-token-revoked"])
		return
	}
	if region != s.cfg.HomeRegionID {
		s.writeJSON(w, s.fixtures["query-wrong-region"])
		return
	}
	next(w, r)
}

func (s *Server) serveQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	size, errSize := strconv.Atoi(q.Get("size"))
	page, errPage := strconv.Atoi(q.Get("pageNumber"))
	start, errStart := time.Parse("20060102", q.Get("startDay"))
	end, errEnd := time.Parse("20060102", q.Get("endDay"))
	if errSize != nil || errPage != nil || errStart != nil || errEnd != nil || size < 1 || page < 1 || q.Get("modeList") != "0" {
		http.Error(w, "malformed query parameters: "+r.URL.RawQuery, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	var matching []Activity
	for _, a := range s.activities {
		day, _ := time.Parse("20060102", a.Start.UTC().Format("20060102"))
		if !day.Before(start) && !day.After(end) {
			matching = append(matching, a)
		}
	}
	s.mu.Unlock()
	slices.SortFunc(matching, func(a, b Activity) int { return b.Start.Compare(a.Start) })

	totalPage := int(math.Ceil(float64(len(matching)) / float64(size)))
	resp := cloneJSON(s.fixtures["query-activities-page-1"])
	data := resp["data"].(map[string]any)
	template := data["dataList"].([]any)[0].(map[string]any)
	data["count"] = len(matching)
	data["pageNumber"] = page
	data["totalPage"] = totalPage
	delete(data, "dataList")

	if page <= totalPage {
		from := (page - 1) * size
		to := min(from+size, len(matching))
		items := make([]any, 0, to-from)
		for _, a := range matching[from:to] {
			item := maps.Clone(template)
			item["labelId"] = a.LabelID
			item["sportType"] = a.SportType
			item["name"] = a.Name
			item["startTime"] = a.Start.Unix()
			item["endTime"] = a.Start.Add(time.Hour).Unix()
			date, _ := strconv.Atoi(a.Start.UTC().Format("20060102"))
			item["date"] = date
			items = append(items, item)
		}
		data["dataList"] = items
	}
	s.writeJSON(w, resp)
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	labelID := q.Get("labelId")
	if q.Get("fileType") != "4" || labelID == "" || q.Get("sportType") == "" {
		http.Error(w, "malformed download parameters: "+r.URL.RawQuery, http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	a, ok := s.activities[labelID]
	s.mu.Unlock()
	if !ok || strconv.Itoa(a.SportType) != q.Get("sportType") {
		http.Error(w, "unknown activity", http.StatusBadRequest)
		return
	}
	if a.NoFile {
		s.writeJSON(w, s.fixtures["download-no-file.synthetic"])
		return
	}
	resp := cloneJSON(s.fixtures["download-fit"])
	data := resp["data"].(map[string]any)
	fixtureURL := data["fileUrl"].(string)
	if !strings.HasPrefix(fixtureURL, fixtureFileHost) {
		s.t.Errorf("fakecoros: fixture fileUrl %q does not use %s", fixtureURL, fixtureFileHost)
	}
	data["fileUrl"] = s.base + "/files/" + labelID + ".fit"
	s.writeJSON(w, resp)
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request, labelID string) {
	if r.Method != http.MethodGet || r.Header.Get("accessToken") != "" {
		http.Error(w, "file downloads are unauthenticated GETs", http.StatusBadRequest)
		return
	}
	fault := s.receive(File, 0, labelID)
	if fault != Stall && fault != Truncate && s.fail(w, r, fault) {
		return
	}
	s.mu.Lock()
	a, ok := s.activities[labelID]
	if ok {
		s.fileGets[labelID]++
	}
	s.mu.Unlock()
	if !ok || a.NoFile {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if fault == Truncate || fault == Stall {
		w.Header().Set("Content-Length", strconv.Itoa(len(a.Content)))
		_, _ = w.Write(a.Content[:len(a.Content)/2])
		if fault == Stall {
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
		}
		return
	}
	_, _ = w.Write(a.Content)
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	s.writeJSONStatus(w, http.StatusOK, v)
}

func (s *Server) writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.t.Errorf("fakecoros: encode response: %v", err)
	}
}

func cloneJSON(v map[string]any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}
