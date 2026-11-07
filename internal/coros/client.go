package coros

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	resultOK             = "0000"
	resultInvalidToken   = "1019"
	resultBadCredentials = "1030"
	loginRegion          = 1
	fitFileType          = "4"
	defaultPageSize      = 50
)

var (
	ErrBadCredentials = errors.New("COROS rejected the email or password")
	ErrInvalidToken   = errors.New("COROS rejected the access token")
	ErrUnknownRegion  = errors.New("unknown COROS region")
)

type ResultError struct {
	Endpoint string
	Code     string
	Message  string
}

func (e *ResultError) Error() string {
	return fmt.Sprintf("COROS %s returned result %s: %s", e.Endpoint, e.Code, e.Message)
}

type HTTPError struct {
	Endpoint string
	Status   int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("COROS %s returned HTTP %d", e.Endpoint, e.Status)
}

type Hosts map[int]string

func DefaultHosts() Hosts {
	return Hosts{
		1: "https://teamapi.coros.com",
		2: "https://teamcnapi.coros.com",
		3: "https://teameuapi.coros.com",
	}
}

func ParseHosts(s string) (Hosts, error) {
	hosts := Hosts{}
	for pair := range strings.SplitSeq(s, ",") {
		idText, host, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || host == "" {
			return nil, fmt.Errorf("invalid host mapping %q, expected <regionId>=<url>", pair)
		}
		id, err := strconv.Atoi(idText)
		if err != nil {
			return nil, fmt.Errorf("invalid region id in %q: %w", pair, err)
		}
		if _, dup := hosts[id]; dup {
			return nil, fmt.Errorf("region %d mapped twice", id)
		}
		hosts[id] = strings.TrimRight(host, "/")
	}
	return hosts, nil
}

type Client struct {
	PageSize int
	hosts    Hosts
	http     *http.Client
}

func NewClient(hosts Hosts, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{PageSize: defaultPageSize, hosts: hosts, http: httpClient}
}

type LoginResult struct {
	Token    string
	RegionID int
}

func (c *Client) HomeHost(regionID int) (string, error) {
	host, ok := c.hosts[regionID]
	if !ok {
		return "", fmt.Errorf("%w %d", ErrUnknownRegion, regionID)
	}
	return host, nil
}

func (c *Client) Login(ctx context.Context, email, password string) (LoginResult, error) {
	host, err := c.HomeHost(loginRegion)
	if err != nil {
		return LoginResult{}, err
	}
	sum := md5.Sum([]byte(password))
	body, err := json.Marshal(map[string]any{
		"account":     email,
		"accountType": 2,
		"pwd":         hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return LoginResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/account/login", bytes.NewReader(body))
	if err != nil {
		return LoginResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	var data struct {
		AccessToken string `json:"accessToken"`
		RegionID    int    `json:"regionId"`
	}
	if err := c.do(req, "login", &data); err != nil {
		var resultErr *ResultError
		if errors.As(err, &resultErr) && resultErr.Code == resultBadCredentials {
			return LoginResult{}, ErrBadCredentials
		}
		return LoginResult{}, err
	}
	if data.AccessToken == "" {
		return LoginResult{}, errors.New("COROS login returned no access token")
	}
	return LoginResult{Token: data.AccessToken, RegionID: data.RegionID}, nil
}

func (c *Client) Session(host, token string) *Session {
	return &Session{client: c, host: strings.TrimRight(host, "/"), token: token}
}

func (c *Client) Fetch(ctx context.Context, fileURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, &HTTPError{Endpoint: "file download", Status: resp.StatusCode}
	}
	return resp.Body, nil
}

type envelope struct {
	Result  string          `json:"result"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(req *http.Request, endpoint string, data any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("COROS %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Endpoint: endpoint, Status: resp.StatusCode}
	}
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("COROS %s: decode response: %w", endpoint, err)
	}
	switch env.Result {
	case resultOK:
	case resultInvalidToken:
		return fmt.Errorf("COROS %s: %w", endpoint, ErrInvalidToken)
	default:
		return &ResultError{Endpoint: endpoint, Code: env.Result, Message: env.Message}
	}
	if data == nil {
		return nil
	}
	if len(env.Data) == 0 {
		return fmt.Errorf("COROS %s: response has no data", endpoint)
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("COROS %s: decode data: %w", endpoint, err)
	}
	return nil
}

type Activity struct {
	LabelID   string `json:"labelId"`
	SportType int    `json:"sportType"`
	Name      string `json:"name"`
	Date      int    `json:"date"`
	StartTime int64  `json:"startTime"`
}

type Session struct {
	client *Client
	host   string
	token  string
}

func (s *Session) Host() string {
	return s.host
}

func (s *Session) Verify(ctx context.Context) error {
	today := time.Now()
	_, err := s.queryPage(ctx, today, today, 1, 1)
	return err
}

func (s *Session) ListActivities(ctx context.Context, from, to time.Time) ([]Activity, error) {
	var all []Activity
	for page := 1; ; page++ {
		p, err := s.queryPage(ctx, from, to, page, s.client.PageSize)
		if err != nil {
			return nil, err
		}
		all = append(all, p.DataList...)
		if page >= p.TotalPage {
			return all, nil
		}
	}
}

type activityPage struct {
	PageNumber int        `json:"pageNumber"`
	TotalPage  int        `json:"totalPage"`
	DataList   []Activity `json:"dataList"`
}

func (s *Session) queryPage(ctx context.Context, from, to time.Time, page, size int) (activityPage, error) {
	q := url.Values{}
	q.Set("size", strconv.Itoa(size))
	q.Set("pageNumber", strconv.Itoa(page))
	q.Set("modeList", "0")
	q.Set("startDay", from.Format("20060102"))
	q.Set("endDay", to.Format("20060102"))
	req, err := s.newRequest(ctx, http.MethodGet, "/activity/query", q)
	if err != nil {
		return activityPage{}, err
	}
	var p activityPage
	if err := s.client.do(req, "activity query", &p); err != nil {
		return activityPage{}, err
	}
	return p, nil
}

func (s *Session) DownloadURL(ctx context.Context, a Activity) (string, error) {
	q := url.Values{}
	q.Set("labelId", a.LabelID)
	q.Set("sportType", strconv.Itoa(a.SportType))
	q.Set("fileType", fitFileType)
	req, err := s.newRequest(ctx, http.MethodPost, "/activity/detail/download", q)
	if err != nil {
		return "", err
	}
	var data struct {
		FileURL string `json:"fileUrl"`
	}
	if err := s.client.do(req, "activity download", &data); err != nil {
		return "", err
	}
	if data.FileURL == "" {
		return "", &ResultError{Endpoint: "activity download", Code: resultOK, Message: "no fileUrl in response"}
	}
	return data.FileURL, nil
}

func (s *Session) newRequest(ctx context.Context, method, path string, q url.Values) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.host+path+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accessToken", s.token)
	return req, nil
}
