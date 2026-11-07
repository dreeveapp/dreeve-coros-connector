//go:build live

package coros_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
)

func TestLiveContract(t *testing.T) {
	liveEmail, livePassword := os.Getenv("COROS_EMAIL"), os.Getenv("COROS_PASSWORD")
	if liveEmail == "" || livePassword == "" {
		t.Fatal("set COROS_EMAIL and COROS_PASSWORD to run the live contract check")
	}
	ctx := context.Background()
	client := coros.NewClient(coros.DefaultHosts())

	if _, err := client.Login(ctx, liveEmail, livePassword+"-wrong"); !errors.Is(err, coros.ErrBadCredentials) {
		t.Fatalf("bad credentials: expected ErrBadCredentials, got %v", err)
	}

	first, err := client.Login(ctx, liveEmail, livePassword)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	home, err := client.HomeHost(first.RegionID)
	if err != nil {
		t.Fatalf("home host: %v", err)
	}
	session := client.Session(home, first.Token)
	if err := session.Verify(ctx); err != nil {
		t.Fatalf("verify on home host: %v", err)
	}

	wrongHost := coros.DefaultHosts()[3]
	if first.RegionID == 3 {
		wrongHost = coros.DefaultHosts()[1]
	}
	if err := client.Session(wrongHost, first.Token).Verify(ctx); !errors.Is(err, coros.ErrInvalidToken) {
		t.Fatalf("wrong region: expected ErrInvalidToken, got %v", err)
	}

	to := time.Now()
	from := to.AddDate(0, 0, -90)
	client.PageSize = 2
	activities, err := session.ListActivities(ctx, from, to)
	if err != nil {
		t.Fatalf("list activities: %v", err)
	}
	if len(activities) < 3 {
		t.Fatalf("need at least 3 activities in the last 90 days to exercise pagination, got %d", len(activities))
	}

	assertActivityKeysMatchFixture(t, home, first.Token, from, to)

	fileURL, err := session.DownloadURL(ctx, activities[0])
	if err != nil {
		t.Fatalf("download url: %v", err)
	}
	body, err := client.Fetch(ctx, fileURL)
	if err != nil {
		t.Fatalf("fetch file: %v", err)
	}
	header := make([]byte, 12)
	_, err = io.ReadFull(body, header)
	_ = body.Close()
	if err != nil || !bytes.Equal(header[8:12], []byte(".FIT")) {
		t.Fatalf("downloaded file is not a FIT file (header %q, err %v)", header, err)
	}

	if _, err := client.Login(ctx, liveEmail, livePassword); err != nil {
		t.Fatalf("second login: %v", err)
	}
	if err := session.Verify(ctx); !errors.Is(err, coros.ErrInvalidToken) {
		t.Fatalf("revoked token: expected ErrInvalidToken, got %v", err)
	}
}

func assertActivityKeysMatchFixture(t *testing.T, host, token string, from, to time.Time) {
	t.Helper()
	fixture := activityKeys(t, mustRead(t, "../../testdata/coros/query-activities-page-1.json"))

	q := url.Values{"size": {"1"}, "pageNumber": {"1"}, "modeList": {"0"}, "startDay": {from.Format("20060102")}, "endDay": {to.Format("20060102")}}
	req, err := http.NewRequest(http.MethodGet, host+"/activity/query?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("accessToken", token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	live := activityKeys(t, mustReadAll(t, resp.Body))

	for key := range fixture {
		if !live[key] {
			t.Errorf("activity field %q is in the fixture but no longer returned by COROS", key)
		}
	}
	for key := range live {
		if !fixture[key] {
			t.Logf("COROS returns a new activity field %q, consider recapturing the fixtures", key)
		}
	}
}

func activityKeys(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	var page struct {
		Data struct {
			DataList []map[string]json.RawMessage `json:"dataList"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &page); err != nil || len(page.Data.DataList) == 0 {
		t.Fatalf("no activity in query response: %v", err)
	}
	keys := map[string]bool{}
	for k := range page.Data.DataList[0] {
		keys[k] = true
	}
	return keys
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustReadAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
