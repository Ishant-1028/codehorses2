package cf

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return New(
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithInterval(1*time.Millisecond),
		WithMaxRetries(0),
	)
}

func TestClient_ContestListOK(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/contest.list") {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(envelope{
			Status: "OK",
			Result: mustJSON(t, []Contest{{ID: 1, Name: "Round 1", Phase: PhaseFinished}}),
		})
	})
	got, err := c.ContestList(context.Background(), false)
	if err != nil {
		t.Fatalf("ContestList: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
}

func TestClient_ContestListFailedIsAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(envelope{
			Status:  "FAILED",
			Comment: "contestId: field is empty",
		})
	})
	_, err := c.ContestList(context.Background(), false)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, ok := err.(*APIError); !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
}

func TestClient_RateLimitRetries(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(envelope{
				Status:  "FAILED",
				Comment: "Call limit exceeded",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(envelope{
			Status: "OK",
			Result: mustJSON(t, []Contest{{ID: 42}}),
		})
	})
	c.maxRetry = 2
	got, err := c.ContestList(context.Background(), false)
	if err != nil {
		t.Fatalf("ContestList: %v", err)
	}
	if len(got) != 1 || got[0].ID != 42 {
		t.Fatalf("bad result after retry: %+v", got)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls, got %d", calls)
	}
}

func TestClient_StandingsSendsHandles(t *testing.T) {
	var seen string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("handles")
		_ = json.NewEncoder(w).Encode(envelope{
			Status: "OK",
			Result: mustJSON(t, Standings{Contest: Contest{ID: 100}}),
		})
	})
	_, err := c.ContestStandings(context.Background(), StandingsRequest{
		ContestID: 100,
		Handles:   []string{"tourist", "jiangly", "Um_nik"},
	})
	if err != nil {
		t.Fatalf("ContestStandings: %v", err)
	}
	if seen != "tourist;jiangly;Um_nik" {
		t.Fatalf("bad handles query: %q", seen)
	}
}

func TestClient_StandingsRejectsOverBatch(t *testing.T) {
	c := New(WithMaxRetries(0))
	handles := make([]string, MaxBatchHandles+1)
	for i := range handles {
		handles[i] = "x"
	}
	_, err := c.ContestStandings(context.Background(), StandingsRequest{
		ContestID: 1,
		Handles:   handles,
	})
	if err == nil {
		t.Fatal("expected batch-cap error")
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
