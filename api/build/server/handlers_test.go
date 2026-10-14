package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeStore struct {
	err     error
	filters Filters
	calls   int
}

func (s *fakeStore) Status(context.Context) (Release, error) {
	s.calls++
	return Release{ReleaseID: "release", SampleData: true, SourceName: "demo_fixture"}, s.err
}
func (s *fakeStore) Seasons(context.Context) (Envelope[Season], error) {
	s.calls++
	return Envelope[Season]{ReleaseID: "release", Data: []Season{}}, s.err
}
func (s *fakeStore) Players(_ context.Context, f Filters) (Envelope[Player], error) {
	s.calls++
	s.filters = f
	return Envelope[Player]{ReleaseID: "release", Data: []Player{{PlayerID: "sample", DisplayName: "Sample", GamesPlayed: 2}}}, s.err
}
func (s *fakeStore) Teams(_ context.Context, f Filters) (Envelope[Team], error) {
	s.calls++
	s.filters = f
	return Envelope[Team]{ReleaseID: "release", Data: []Team{}}, s.err
}

func request(store *fakeStore, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	NewHandler(store).ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

func TestLiveDoesNotNeedDatabase(t *testing.T) {
	s := &fakeStore{err: errors.New("unavailable")}
	w := request(s, "GET", "/health/live")
	if w.Code != 200 || s.calls != 0 || !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Fatalf("live: %d %s", w.Code, w.Body.String())
	}
}

func TestValidationRejectsRequestsBeforeQuerying(t *testing.T) {
	paths := []string{
		"/api/v1/players", "/api/v1/players?season=2024&phase=preseason",
		"/api/v1/players?season=2024&limit=1001", "/api/v1/players?season=2024&limit=0",
		"/api/v1/players?season=2024&sort=pts;DROP%20TABLE%20analytics.dim_player",
		"/api/v1/players?season=2024&season=2025", "/api/v1/players?season=2024&extra=x",
		"/api/v1/comparisons/players?season=2024&ids=a", "/api/v1/comparisons/players?season=2024&ids=a,a",
		"/api/v1/comparisons/players?season=2024&ids=a,b,c,d,e,f",
		"/api/v1/comparisons/teams?season=2024&ids=BOS:2024,LAL:2024&q=x",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			s := &fakeStore{}
			w := request(s, "GET", path)
			if w.Code != 400 || s.calls != 0 {
				t.Fatalf("got %d, %d calls: %s", w.Code, s.calls, w.Body.String())
			}
			if w.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatal("wrong problem type")
			}
		})
	}
}

func TestParametersAndNullMetrics(t *testing.T) {
	s := &fakeStore{}
	w := request(s, "GET", "/api/v1/players?season=2024&phase=playoffs&q=sample&sort=ast_per_game&limit=17")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.filters.Season != 2024 || s.filters.Phase != "playoffs" || s.filters.Sort != "ast_per_game" || s.filters.Limit != 17 {
		t.Fatalf("bad filters: %+v", s.filters)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	row := body["data"].([]any)[0].(map[string]any)
	if row["pts"] != nil || row["pts_per_game"] != nil || row["games_played"] != float64(2) {
		t.Fatalf("wrong numeric/null semantics: %+v", row)
	}
}

func TestComparisonKeepsRequestedIDs(t *testing.T) {
	s := &fakeStore{}
	w := request(s, "GET", "/api/v1/comparisons/players?season=2024&ids=second,first")
	if w.Code != 200 || len(s.filters.IDs) != 2 || s.filters.IDs[0] != "second" || s.filters.IDs[1] != "first" || s.filters.Limit != 5 {
		t.Fatalf("bad comparison: %d %+v", w.Code, s.filters)
	}
}

func TestBootstrapAndInternalErrorsAreSafe(t *testing.T) {
	for _, err := range []error{ErrNoRelease, errors.New("password=secret postgres internal SQL error")} {
		s := &fakeStore{err: err}
		w := request(s, "GET", "/api/v1/status")
		if w.Code != 503 || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Retry-After") != "30" {
			t.Fatalf("unsafe/unexpected: %d %s", w.Code, w.Body.String())
		}
	}
	w := request(&fakeStore{err: ErrMissingEntity}, "GET", "/api/v1/comparisons/teams?season=2024&ids=BOS:2024,LAL:2024")
	if w.Code != 404 {
		t.Fatal(w.Body.String())
	}
}

func TestMethodAndUnknownRoute(t *testing.T) {
	w := request(&fakeStore{}, "POST", "/api/v1/status")
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
		t.Fatal(w.Code)
	}
	w = request(&fakeStore{}, "GET", "/not-an-endpoint")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
