package cf

import (
	"context"
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

// DefaultBaseURL is the public Codeforces API root.
const DefaultBaseURL = "https://codeforces.com/api"

// DefaultInterval is the API's advertised rate limit: 1 request per 2s.
const DefaultInterval = 2 * time.Second

// MaxBatchHandles is the ceiling documented for user.info / contest.standings
// handle-batch queries. Exceeding this returns a FAILED response.
const MaxBatchHandles = 10000

// Client is the HTTP client for the Codeforces API. It is safe for
// concurrent use — the shared limiter enforces the global rate limit.
type Client struct {
	httpc    *http.Client
	baseURL  string
	limiter  *rateLimiter
	signer   signer
	maxRetry int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient overrides the transport (useful for tests / proxies).
func WithHTTPClient(c *http.Client) Option { return func(cl *Client) { cl.httpc = c } }

// WithBaseURL overrides the API root.
func WithBaseURL(u string) Option { return func(cl *Client) { cl.baseURL = u } }

// WithInterval overrides the per-request minimum interval. The CF default
// is 2 seconds; do not lower it in production.
func WithInterval(d time.Duration) Option {
	return func(cl *Client) { cl.limiter = newRateLimiter(d) }
}

// WithAuth attaches API key + secret so authenticated endpoints can be
// used (e.g. contest.standings with `handles`).
func WithAuth(key, secret string) Option {
	return func(cl *Client) { cl.signer = signer{key: key, secret: secret} }
}

// WithMaxRetries controls how many times a transient failure is retried
// with exponential backoff. Defaults to 3.
func WithMaxRetries(n int) Option { return func(cl *Client) { cl.maxRetry = n } }

// New builds a client with sensible defaults.
func New(opts ...Option) *Client {
	c := &Client{
		httpc:    &http.Client{Timeout: 30 * time.Second},
		baseURL:  DefaultBaseURL,
		limiter:  newRateLimiter(DefaultInterval),
		maxRetry: 3,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// APIError is a "FAILED" response from Codeforces.
type APIError struct {
	Method  string
	Comment string
}

func (e *APIError) Error() string { return fmt.Sprintf("codeforces %s: %s", e.Method, e.Comment) }

// IsRateLimit returns true when the error is CF's "Call limit exceeded"
// signal. Callers should back off and retry — the limiter should already
// prevent this, but we defend in depth.
func IsRateLimit(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return strings.Contains(strings.ToLower(apiErr.Comment), "call limit")
	}
	return false
}

// envelope matches the standard `{status, result, comment}` CF response.
type envelope struct {
	Status  string          `json:"status"`
	Comment string          `json:"comment,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (c *Client) call(ctx context.Context, method string, params url.Values, out any) error {
	if params == nil {
		params = url.Values{}
	}
	var lastErr error
	for attempt := 0; attempt <= c.maxRetry; attempt++ {
		if attempt > 0 {
			// backoff: 1s, 2s, 4s, ... capped at 30s
			delay := time.Duration(1<<attempt) * time.Second
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		signed, err := c.signer.sign(method, params)
		if err != nil {
			return err
		}
		if err := c.limiter.Wait(ctx); err != nil {
			return err
		}

		reqURL := c.baseURL + "/" + method + "?" + signed.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpc.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("codeforces %s: http %d", method, resp.StatusCode)
			continue
		}

		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return fmt.Errorf("codeforces %s: decode envelope: %w (body=%q)", method, err, truncate(body, 200))
		}
		if env.Status != "OK" {
			apiErr := &APIError{Method: method, Comment: env.Comment}
			if IsRateLimit(apiErr) {
				lastErr = apiErr
				continue
			}
			return apiErr
		}
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("codeforces %s: decode result: %w", method, err)
		}
		return nil
	}
	return lastErr
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}

// ---------------------------------------------------------------------
// Public endpoints
// ---------------------------------------------------------------------

// ContestList returns all contests (default: past + upcoming). Set gym
// to true to include gyms.
func (c *Client) ContestList(ctx context.Context, gym bool) ([]Contest, error) {
	params := url.Values{}
	if gym {
		params.Set("gym", "true")
	}
	var out []Contest
	if err := c.call(ctx, "contest.list", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// StandingsRequest configures contest.standings.
type StandingsRequest struct {
	ContestID      int
	From           int      // 1-indexed
	Count          int      // 0 → server default
	Handles        []string // if non-empty, filter to just these
	Room           int      // 0 → all rooms
	ShowUnofficial bool
}

// ContestStandings returns the leaderboard for one contest. When Handles
// is provided the batch cap is MaxBatchHandles (10,000) — the caller must
// split larger sets across multiple calls, which the ingest worker does.
func (c *Client) ContestStandings(ctx context.Context, req StandingsRequest) (*Standings, error) {
	if req.ContestID == 0 {
		return nil, errors.New("contest.standings: ContestID required")
	}
	if len(req.Handles) > MaxBatchHandles {
		return nil, fmt.Errorf("contest.standings: %d handles exceeds cap %d",
			len(req.Handles), MaxBatchHandles)
	}
	params := url.Values{}
	params.Set("contestId", strconv.Itoa(req.ContestID))
	if req.From > 0 {
		params.Set("from", strconv.Itoa(req.From))
	}
	if req.Count > 0 {
		params.Set("count", strconv.Itoa(req.Count))
	}
	if len(req.Handles) > 0 {
		params.Set("handles", strings.Join(req.Handles, ";"))
	}
	if req.Room > 0 {
		params.Set("room", strconv.Itoa(req.Room))
	}
	if req.ShowUnofficial {
		params.Set("showUnofficial", "true")
	}
	var out Standings
	if err := c.call(ctx, "contest.standings", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ContestStatus returns submissions for one contest, most recent first.
func (c *Client) ContestStatus(ctx context.Context, contestID, from, count int) ([]Submission, error) {
	params := url.Values{}
	params.Set("contestId", strconv.Itoa(contestID))
	if from > 0 {
		params.Set("from", strconv.Itoa(from))
	}
	if count > 0 {
		params.Set("count", strconv.Itoa(count))
	}
	var out []Submission
	if err := c.call(ctx, "contest.status", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ContestRatingChanges returns the finalised rating deltas for one contest.
// This being non-empty is the "results are truly final" signal — do not
// pay out before it returns entries.
func (c *Client) ContestRatingChanges(ctx context.Context, contestID int) ([]RatingChange, error) {
	params := url.Values{}
	params.Set("contestId", strconv.Itoa(contestID))
	var out []RatingChange
	if err := c.call(ctx, "contest.ratingChanges", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UserInfo fetches multiple users at once (batch cap MaxBatchHandles).
func (c *Client) UserInfo(ctx context.Context, handles []string) ([]User, error) {
	if len(handles) == 0 {
		return nil, nil
	}
	if len(handles) > MaxBatchHandles {
		return nil, fmt.Errorf("user.info: %d handles exceeds cap %d", len(handles), MaxBatchHandles)
	}
	params := url.Values{}
	params.Set("handles", strings.Join(handles, ";"))
	var out []User
	if err := c.call(ctx, "user.info", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UserRating returns the rating change history for one handle.
func (c *Client) UserRating(ctx context.Context, handle string) ([]RatingChange, error) {
	params := url.Values{}
	params.Set("handle", handle)
	var out []RatingChange
	if err := c.call(ctx, "user.rating", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// UserRatedList returns the full list of rated users. Set activeOnly to
// restrict to users active in the past month (much smaller payload).
func (c *Client) UserRatedList(ctx context.Context, activeOnly, includeRetired bool) ([]User, error) {
	params := url.Values{}
	if activeOnly {
		params.Set("activeOnly", "true")
	}
	if includeRetired {
		params.Set("includeRetired", "true")
	}
	var out []User
	if err := c.call(ctx, "user.ratedList", params, &out); err != nil {
		return nil, err
	}
	return out, nil
}
