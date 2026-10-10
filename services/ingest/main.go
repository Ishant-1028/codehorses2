// Command ingest is the single owner of the Codeforces API poll budget.
//
// One instance runs per deployment (do not scale horizontally — you'll
// blow the 0.5 req/sec cap). It maintains a per-contest poll schedule,
// prioritises live contests over pending/finished ones, and writes
// normalised events into a Redis Stream that the scoring service
// consumes.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/g1tsg/codehorses/packages/cf"
	"github.com/g1tsg/codehorses/packages/store"
	"github.com/redis/go-redis/v9"
)

type config struct {
	redisAddr    string
	cfKey        string
	cfSecret     string
	cfInterval   time.Duration
	livePollGap  time.Duration
	discoverGap  time.Duration
	streamMaxLen int64
}

func loadConfig() config {
	c := config{
		redisAddr:    getenv("REDIS_ADDR", "localhost:6379"),
		cfKey:        os.Getenv("CF_API_KEY"),
		cfSecret:     os.Getenv("CF_API_SECRET"),
		cfInterval:   parseDuration(os.Getenv("CF_INTERVAL"), cf.DefaultInterval),
		livePollGap:  parseDuration(os.Getenv("LIVE_POLL_GAP"), 3*time.Second),
		discoverGap:  parseDuration(os.Getenv("DISCOVER_GAP"), 5*time.Minute),
		streamMaxLen: parseInt(os.Getenv("STREAM_MAXLEN"), 100000),
	}
	return c
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis unreachable", "addr", cfg.redisAddr, "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	opts := []cf.Option{cf.WithInterval(cfg.cfInterval)}
	if cfg.cfKey != "" && cfg.cfSecret != "" {
		opts = append(opts, cf.WithAuth(cfg.cfKey, cfg.cfSecret))
	}
	client := cf.New(opts...)

	sup := newSupervisor(client, rdb, cfg)

	// Discovery loop: every 5min, refresh which CF contests we care about.
	go sup.discoverLoop(ctx)

	slog.Info("ingest started", "redis", cfg.redisAddr, "authenticated", cfg.cfKey != "")
	<-ctx.Done()
	slog.Info("ingest shutting down")
	sup.stopAll()
}

// supervisor coordinates one poll goroutine per active contest, sharing
// the single-writer CF client and its rate limiter.
type supervisor struct {
	client *cf.Client
	rdb    *redis.Client
	cfg    config

	mu      sync.Mutex
	workers map[int]context.CancelFunc
}

func newSupervisor(c *cf.Client, rdb *redis.Client, cfg config) *supervisor {
	return &supervisor{
		client:  c,
		rdb:     rdb,
		cfg:     cfg,
		workers: make(map[int]context.CancelFunc),
	}
}

func (s *supervisor) discoverLoop(ctx context.Context) {
	for {
		s.discover(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.cfg.discoverGap):
		}
	}
}

func (s *supervisor) discover(ctx context.Context) {
	contests, err := s.client.ContestList(ctx, false)
	if err != nil {
		slog.Warn("discover: contest.list failed", "err", err)
		return
	}
	now := time.Now().Unix()
	for _, c := range contests {
		if c.Type != "CF" {
			continue
		}
		// Care about contests that are BEFORE / CODING / PENDING /
		// SYSTEM_TEST — and about FINISHED contests whose rating
		// changes have not yet been observed.
		if c.Phase == cf.PhaseFinished && c.StartTimeSeconds < now-24*3600 {
			s.stop(c.ID)
			continue
		}
		s.ensureWorker(ctx, c)
	}
}

func (s *supervisor) ensureWorker(parent context.Context, c cf.Contest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workers[c.ID]; ok {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	s.workers[c.ID] = cancel
	go s.pollContest(ctx, c)
	slog.Info("worker started", "contest_id", c.ID, "phase", c.Phase, "name", c.Name)
}

func (s *supervisor) stop(contestID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.workers[contestID]; ok {
		cancel()
		delete(s.workers, contestID)
	}
}

func (s *supervisor) stopAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, cancel := range s.workers {
		cancel()
		delete(s.workers, id)
	}
}

// pollContest owns one contest's poll cadence for its entire lifecycle.
func (s *supervisor) pollContest(ctx context.Context, seed cf.Contest) {
	state := seed
	backoff := time.Second

	writeEvent := func(ev store.Event) {
		payload := map[string]any{
			"type":       string(ev.Type),
			"contest_id": ev.ContestID,
			"ts":         ev.Timestamp,
			"payload":    ev.PayloadJSON,
		}
		key := store.EventStreamKey(state.ID)
		err := s.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: key,
			MaxLen: s.cfg.streamMaxLen,
			Approx: true,
			Values: payload,
		}).Err()
		if err != nil {
			slog.Warn("XADD failed", "stream", key, "err", err)
		}
	}

	// Emit an initial phase-change event so downstream sees the state on start-up.
	if b, err := json.Marshal(map[string]any{
		"phase":              state.Phase,
		"start_time_seconds": state.StartTimeSeconds,
		"duration_seconds":   state.DurationSeconds,
		"name":               state.Name,
	}); err == nil {
		writeEvent(store.Event{
			Type:        store.EventPhaseChange,
			ContestID:   state.ID,
			Timestamp:   time.Now().UnixMilli(),
			PayloadJSON: string(b),
		})
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		gap := s.pollGap(state.Phase)
		select {
		case <-ctx.Done():
			return
		case <-time.After(gap):
		}

		standings, err := s.client.ContestStandings(ctx, cf.StandingsRequest{
			ContestID: state.ID,
			Count:     500, // top 500 for now; scoring engine uses handle-batch too
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("standings failed", "cf_contest", state.ID, "err", err)
			// Exponential backoff on repeated failure, capped.
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		// Detect phase transition, emit dedicated event.
		if standings.Contest.Phase != state.Phase {
			slog.Info("phase changed", "cf_contest", state.ID,
				"from", state.Phase, "to", standings.Contest.Phase)
			state.Phase = standings.Contest.Phase
			if b, err := json.Marshal(map[string]any{"phase": state.Phase}); err == nil {
				writeEvent(store.Event{
					Type:        store.EventPhaseChange,
					ContestID:   state.ID,
					Timestamp:   time.Now().UnixMilli(),
					PayloadJSON: string(b),
				})
			}
		}

		// Standings tick.
		if b, err := json.Marshal(standings); err == nil {
			writeEvent(store.Event{
				Type:        store.EventStandingsTick,
				ContestID:   state.ID,
				Timestamp:   time.Now().UnixMilli(),
				PayloadJSON: string(b),
			})
		}

		// On FINISHED, wait for ratingChanges to be non-empty before
		// declaring FINAL_RESULTS — that's the CF-side "results are truly
		// final" signal.
		if state.Phase == cf.PhaseFinished {
			changes, err := s.client.ContestRatingChanges(ctx, state.ID)
			if err == nil && len(changes) > 0 {
				if b, err := json.Marshal(changes); err == nil {
					writeEvent(store.Event{
						Type:        store.EventFinalResults,
						ContestID:   state.ID,
						Timestamp:   time.Now().UnixMilli(),
						PayloadJSON: string(b),
					})
				}
				slog.Info("final results emitted", "cf_contest", state.ID, "n", len(changes))
				return
			}
		}
	}
}

// pollGap returns the poll cadence for a phase. Priorities:
//   - LIVE gets tight polls (feel live)
//   - PENDING/SYSTEM_TEST is slower — final results still 15min–2hr away
//   - BEFORE only needs to know when it flips to CODING
func (s *supervisor) pollGap(p cf.ContestPhase) time.Duration {
	switch p {
	case cf.PhaseCoding:
		return s.cfg.livePollGap
	case cf.PhasePendingSystemTest, cf.PhaseSystemTest:
		return 30 * time.Second
	case cf.PhaseFinished:
		return 60 * time.Second
	default:
		return 2 * time.Minute
	}
}

// ---- utils ----

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseDuration(s string, def time.Duration) time.Duration {
	if s == "" {
		return def
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad duration %q, using %v\n", s, def)
		return def
	}
	return d
}

func parseInt(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad int %q, using %d\n", s, def)
		return def
	}
	return n
}
