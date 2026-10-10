// Command scoring consumes the event log written by ingest, materialises
// per-user lineup scores, and pushes leaderboard snapshots to the
// realtime channel.
//
// Scoring is stateless with respect to the log: every tick recomputes
// each affected lineup from the latest standings snapshot. This makes
// the service crash-safe (any state was already in the log) and
// horizontally scalable (multiple instances share a Redis consumer group).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/g1tsg/codehorses/packages/cf"
	scoringrules "github.com/g1tsg/codehorses/packages/scoringrules"
	"github.com/g1tsg/codehorses/packages/store"
	"github.com/redis/go-redis/v9"
)

type config struct {
	redisAddr     string
	consumerName  string
	broadcastGap  time.Duration
	blockDuration time.Duration
}

func loadConfig() config {
	host, _ := os.Hostname()
	return config{
		redisAddr:     getenv("REDIS_ADDR", "localhost:6379"),
		consumerName:  getenv("CONSUMER_NAME", host),
		broadcastGap:  parseDuration(os.Getenv("BROADCAST_GAP"), 500*time.Millisecond),
		blockDuration: parseDuration(os.Getenv("BLOCK_DURATION"), 5*time.Second),
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg := loadConfig()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb := redis.NewClient(&redis.Options{Addr: cfg.redisAddr})
	if err := rdb.Ping(ctx).Err(); err != nil {
		slog.Error("redis unreachable", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	// Optional Postgres (for reading lineups). Absent = demo mode
	// where the scoring engine broadcasts the raw CF top-N as-is.
	var db *sql.DB // hooked up in phase 2 with a real driver

	engine := &engine{
		rdb:  rdb,
		db:   db,
		cfg:  cfg,
		seen: make(map[int]bool),
	}

	// Contest supervisor: watch which streams exist, spawn a consumer
	// goroutine per contest.
	go engine.supervise(ctx)

	slog.Info("scoring started", "redis", cfg.redisAddr, "consumer", cfg.consumerName)
	<-ctx.Done()
	slog.Info("scoring shutting down")
}

type engine struct {
	rdb  *redis.Client
	db   *sql.DB
	cfg  config
	mu   sync.Mutex
	seen map[int]bool
}

// supervise every 30s scans the Redis keyspace for event streams and
// spawns a consumer goroutine per contest. New contests are picked up
// automatically; ones that finish stop producing events and their
// consumer exits when its stream goes quiet.
func (e *engine) supervise(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	e.scanStreams(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			e.scanStreams(ctx)
		}
	}
}

func (e *engine) scanStreams(ctx context.Context) {
	var cursor uint64
	for {
		keys, next, err := e.rdb.Scan(ctx, cursor, "cf.contest.*.events", 100).Result()
		if err != nil {
			slog.Warn("SCAN failed", "err", err)
			return
		}
		for _, k := range keys {
			cid := parseContestIDFromKey(k)
			if cid == 0 {
				continue
			}
			e.mu.Lock()
			if !e.seen[cid] {
				e.seen[cid] = true
				go e.consume(ctx, cid)
			}
			e.mu.Unlock()
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
}

func parseContestIDFromKey(key string) int {
	// key is "cf.contest.<id>.events"
	if !strings.HasPrefix(key, "cf.contest.") || !strings.HasSuffix(key, ".events") {
		return 0
	}
	mid := strings.TrimSuffix(strings.TrimPrefix(key, "cf.contest."), ".events")
	n, _ := strconv.Atoi(mid)
	return n
}

func (e *engine) consume(ctx context.Context, cfContestID int) {
	stream := store.EventStreamKey(cfContestID)
	group := store.ConsumerGroup(cfContestID)

	// Create consumer group if it doesn't exist. BUSYGROUP means it already does.
	if err := e.rdb.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		if !strings.Contains(err.Error(), "BUSYGROUP") {
			slog.Warn("XGROUP CREATE failed", "stream", stream, "err", err)
			return
		}
	}

	slog.Info("consumer started", "contest", cfContestID)
	defer slog.Info("consumer stopped", "contest", cfContestID)

	lastBroadcast := time.Time{}
	var latestStandings *cf.Standings

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		res, err := e.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    group,
			Consumer: e.cfg.consumerName,
			Streams:  []string{stream, ">"},
			Count:    16,
			Block:    e.cfg.blockDuration,
		}).Result()

		if err != nil {
			if err == redis.Nil || err == context.Canceled {
				continue
			}
			// If contest ended and stream is quiet for a long time, we
			// could exit here — for MVP we just keep looping cheaply.
			slog.Warn("XREADGROUP failed", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		var toAck []string
		for _, s := range res {
			for _, msg := range s.Messages {
				toAck = append(toAck, msg.ID)
				etype, _ := msg.Values["type"].(string)
				payload, _ := msg.Values["payload"].(string)

				switch store.EventType(etype) {
				case store.EventStandingsTick:
					var st cf.Standings
					if err := json.Unmarshal([]byte(payload), &st); err != nil {
						slog.Warn("bad standings payload", "err", err)
						continue
					}
					latestStandings = &st
				case store.EventPhaseChange, store.EventFinalResults:
					// Not scored directly; ack-through for now. A future
					// phase settles payouts from a FinalResults event.
				}
			}
		}
		if len(toAck) > 0 {
			e.rdb.XAck(ctx, stream, group, toAck...)
		}

		// Broadcast (debounced) — one snapshot per broadcastGap even if
		// we processed 30 messages in this iteration.
		if latestStandings != nil && time.Since(lastBroadcast) >= e.cfg.broadcastGap {
			e.publish(ctx, cfContestID, latestStandings)
			lastBroadcast = time.Now()
		}
	}
}

// publish updates the leaderboard ZSET and fires a pubsub notification.
//
// At MVP we broadcast the raw CF top-N as the leaderboard, keyed by
// programmer handle. Phase 2 will replace this with fantasy-lineup
// scores read from Postgres.
func (e *engine) publish(ctx context.Context, cfContestID int, st *cf.Standings) {
	if st == nil || len(st.Rows) == 0 {
		return
	}
	internalKey := fmt.Sprintf("cf-%d", cfContestID) // real code maps to internal contest UUID
	zkey := store.LeaderboardKey(internalKey)

	// Score = -rank (so ZREVRANGE returns top ranks first). Points would
	// also work; rank is simpler for the CF standings passthrough.
	pipe := e.rdb.Pipeline()
	pipe.Del(ctx, zkey)
	top := st.Rows
	if len(top) > 200 {
		top = top[:200]
	}
	members := make([]redis.Z, 0, len(top))
	for _, row := range top {
		if len(row.Party.Members) == 0 {
			continue
		}
		handle := row.Party.Members[0].Handle
		members = append(members, redis.Z{Score: -float64(row.Rank), Member: handle})
	}
	if len(members) > 0 {
		pipe.ZAdd(ctx, zkey, members...)
	}
	pipe.Expire(ctx, zkey, 24*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Warn("ZADD leaderboard failed", "err", err)
		return
	}

	// Fire a compact notification. Gateway subscribers pull top-N via ZSET
	// after this arrives — this keeps the pubsub payload tiny.
	payload := map[string]any{
		"contest_cf_id": cfContestID,
		"ts":            time.Now().UnixMilli(),
		"rank_count":    len(members),
		"scoring_rules": int(scoringrules.Latest),
	}
	if b, err := json.Marshal(payload); err == nil {
		e.rdb.Publish(ctx, store.LeaderboardChannel(internalKey), string(b))
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
		return def
	}
	return d
}
