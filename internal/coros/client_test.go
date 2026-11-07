package coros_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/xballoy/dreeve-coros-connector/internal/coros"
	"github.com/xballoy/dreeve-coros-connector/internal/fakecoros"
)

const (
	email    = "runner@example.com"
	password = "correct horse"
)

var day = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func newFake(t *testing.T, regionID, homeRegionID int) (*fakecoros.Server, *coros.Client) {
	t.Helper()
	fake := fakecoros.New(t, fakecoros.Config{
		FixturesDir:  "../../testdata/coros",
		Email:        email,
		Password:     password,
		RegionID:     regionID,
		HomeRegionID: homeRegionID,
	})
	hosts, err := coros.ParseHosts(fake.HostsEnv())
	if err != nil {
		t.Fatalf("ParseHosts: %v", err)
	}
	return fake, coros.NewClient(hosts)
}

func login(t *testing.T, fake *fakecoros.Server, client *coros.Client) *coros.Session {
	t.Helper()
	result, err := client.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	host, err := client.HomeHost(result.RegionID)
	if err != nil {
		t.Fatalf("HomeHost: %v", err)
	}
	return client.Session(host, result.Token)
}

func TestLoginReturnsTokenAndRegion(t *testing.T) {
	fake, client := newFake(t, 3, 3)
	result, err := client.Login(context.Background(), email, password)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if result.Token == "" || result.RegionID != 3 {
		t.Fatalf("unexpected login result %+v", result)
	}
	host, err := client.HomeHost(3)
	if err != nil || host != fake.RegionURL(3) {
		t.Fatalf("HomeHost(3) = %q, %v", host, err)
	}
}

func TestLoginWithWrongPasswordIsBadCredentials(t *testing.T) {
	_, client := newFake(t, 1, 1)
	_, err := client.Login(context.Background(), email, "wrong")
	if !errors.Is(err, coros.ErrBadCredentials) {
		t.Fatalf("expected ErrBadCredentials, got %v", err)
	}
}

func TestHomeHostRejectsUnknownRegion(t *testing.T) {
	_, client := newFake(t, 1, 1)
	if _, err := client.HomeHost(7); !errors.Is(err, coros.ErrUnknownRegion) {
		t.Fatalf("expected ErrUnknownRegion, got %v", err)
	}
}

func TestVerifySucceedsOnHomeHost(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	if err := login(t, fake, client).Verify(context.Background()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyOnWrongRegionIsInvalidToken(t *testing.T) {
	fake, client := newFake(t, 1, 3)
	err := login(t, fake, client).Verify(context.Background())
	if !errors.Is(err, coros.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestRevokedTokenIsInvalidToken(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	session := login(t, fake, client)
	fake.RevokeTokens()
	_, err := session.ListActivities(context.Background(), day, day)
	if !errors.Is(err, coros.ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got %v", err)
	}
}

func TestListActivitiesWalksEveryPageWithinTheWindow(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	for i, offset := range []int{0, 1, 2, 3, 4, 10} {
		fake.AddActivity(fakecoros.Activity{
			LabelID:   "90000000000000000" + string(rune('1'+i)),
			SportType: 100,
			Name:      "Run",
			Start:     day.AddDate(0, 0, -offset),
		})
	}
	client.PageSize = 2

	got, err := login(t, fake, client).ListActivities(context.Background(), day.AddDate(0, 0, -4), day)
	if err != nil {
		t.Fatalf("ListActivities: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 activities across 3 pages, got %d: %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a.LabelID] {
			t.Fatalf("activity %s listed twice", a.LabelID)
		}
		seen[a.LabelID] = true
		if a.SportType != 100 || a.Name != "Run" {
			t.Fatalf("activity decoded wrongly: %+v", a)
		}
	}
	if seen["900000000000000006"] {
		t.Fatal("activity outside the window was listed")
	}
}

func TestListActivitiesWithNoActivitiesIsEmpty(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	got, err := login(t, fake, client).ListActivities(context.Background(), day, day)
	if err != nil || len(got) != 0 {
		t.Fatalf("expected no activities, got %v, %v", got, err)
	}
}

func TestDownloadFetchesTheFitFile(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	fake.AddActivity(fakecoros.Activity{LabelID: "900000000000000002", SportType: 101, Start: day, Content: []byte("fit bytes")})
	session := login(t, fake, client)

	url, err := session.DownloadURL(context.Background(), coros.Activity{LabelID: "900000000000000002", SportType: 101})
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	body, err := client.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "fit bytes" {
		t.Fatalf("unexpected file content %q", got)
	}
}

func TestDownloadWithoutFileReportsTheResultCode(t *testing.T) {
	fake, client := newFake(t, 1, 1)
	fake.AddActivity(fakecoros.Activity{LabelID: "900000000000000002", SportType: 900, Start: day, NoFile: true})

	_, err := login(t, fake, client).DownloadURL(context.Background(), coros.Activity{LabelID: "900000000000000002", SportType: 900})
	var resultErr *coros.ResultError
	if !errors.As(err, &resultErr) || resultErr.Code != "9999" {
		t.Fatalf("expected a ResultError with code 9999, got %v", err)
	}
}

func TestParseHostsRejectsMalformedInput(t *testing.T) {
	for _, input := range []string{"1", "x=http://a", "1=", "1=http://a,1=http://b"} {
		if _, err := coros.ParseHosts(input); err == nil {
			t.Errorf("expected an error for %q", input)
		}
	}
}
