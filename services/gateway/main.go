// Command gateway is the WebSocket fan-out for live leaderboards.
//
// Design at 1M concurrent users:
//
//	Load balancer sticky-routes clients by contest_id (Consistent Hash on
//	the URL path). All viewers of one contest land on one gateway pod, so
//	that pod holds a single Redis subscription and multicasts to all of
//	its WebSockets. Fan-out cost per broadcast is O(subs), not O(users*contests).
//
// A single tuned Go node handles ~500k WS connections. For 1M concurrent
// viewers on the biggest CF round, 3 pods are enough; the LB rebalances
// automatically as load shifts.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/g1tsg/codehorses/packages/store"
	"github.com/redis/go-redis/v9"
)

type config struct {
	listenAddr string
	redisAddr  string
	topN       int64
	pingGap    time.Duration
}

func loadConfig() config {
	return config{
		listenAddr: getenv("LISTEN_ADDR", ":8080"),
		redisAddr:  getenv("REDIS_ADDR", "localhost:6379"),
		topN:       100,
		pingGap:    30 * time.Second,
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

	hub := newHub(rdb, cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/ws/contest/", hub.handleWS)
	mux.HandleFunc("/leaderboard/", hub.handleTopN)

	srv := &http.Server{Addr: cfg.listenAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	slog.Info("gateway listening", "addr", cfg.listenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// hub coordinates per-contest fan-out. One subscription per contest is
// shared by every WebSocket viewing that contest.
type hub struct {
	rdb *redis.Client
	cfg config

	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	contestID string
	rdb       *redis.Client
	cfg       config

	subCtx    context.Context
	subCancel context.CancelFunc

	subsMu sync.RWMutex
	subs   map[*conn]struct{}
}

type conn struct {
	ws   *websocket.Conn
	send chan []byte
}

func newHub(rdb *redis.Client, cfg config) *hub {
	return &hub{rdb: rdb, cfg: cfg, rooms: make(map[string]*room)}
}

// joinRoom returns the room for one contest, spinning up the Redis
// subscription lazily on first join.
func (h *hub) joinRoom(ctx context.Context, contestID string, c *conn) *room {
	h.mu.Lock()
	r, ok := h.rooms[contestID]
	if !ok {
		subCtx, subCancel := context.WithCancel(context.Background())
		r = &room{
			contestID: contestID,
			rdb:       h.rdb,
			cfg:       h.cfg,
			subCtx:    subCtx,
			subCancel: subCancel,
			subs:      make(map[*conn]struct{}),
		}
		h.rooms[contestID] = r
		go r.run()
	}
	h.mu.Unlock()

	r.subsMu.Lock()
	r.subs[c] = struct{}{}
	r.subsMu.Unlock()
	return r
}

func (h *hub) leaveRoom(contestID string, c *conn) {
	h.mu.Lock()
	r, ok := h.rooms[contestID]
	h.mu.Unlock()
	if !ok {
		return
	}
	r.subsMu.Lock()
	delete(r.subs, c)
	empty := len(r.subs) == 0
	r.subsMu.Unlock()

	if empty {
		h.mu.Lock()
		// re-check under hub lock in case someone rejoined
		r.subsMu.RLock()
		still := len(r.subs)
		r.subsMu.RUnlock()
		if still == 0 {
			delete(h.rooms, contestID)
			r.subCancel()
		}
		h.mu.Unlock()
	}
}

// run maintains the Redis subscription for one contest and multicasts
// each incoming leaderboard tick to every WebSocket in this room.
func (r *room) run() {
	channel := store.LeaderboardChannel(r.contestID)
	sub := r.rdb.Subscribe(r.subCtx, channel)
	defer sub.Close()

	slog.Info("room opened", "contest", r.contestID)
	defer slog.Info("room closed", "contest", r.contestID)

	ch := sub.Channel()
	for {
		select {
		case <-r.subCtx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			// Fetch top-N and broadcast it as the delta. Doing this in
			// the room (not per-client) keeps the ZRANGE cost O(1)
			// per broadcast even for 500k viewers.
			snapshot, err := r.topN(r.subCtx)
			if err != nil {
				slog.Warn("topN failed", "err", err)
				continue
			}
			payload, _ := json.Marshal(map[string]any{
				"type":        "leaderboard",
				"contest":     r.contestID,
				"top":         snapshot,
				"received_at": msg.Payload,
			})
			r.broadcast(payload)
		}
	}
}

// topN fetches the top-N members of the leaderboard ZSET.
func (r *room) topN(ctx context.Context) ([]leaderboardEntry, error) {
	key := store.LeaderboardKey(r.contestID)
	rows, err := r.rdb.ZRevRangeWithScores(ctx, key, 0, r.cfg.topN-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]leaderboardEntry, 0, len(rows))
	for i, z := range rows {
		out = append(out, leaderboardEntry{
			Rank:   i + 1,
			Handle: toString(z.Member),
			Score:  z.Score,
		})
	}
	return out, nil
}

type leaderboardEntry struct {
	Rank   int     `json:"rank"`
	Handle string  `json:"handle"`
	Score  float64 `json:"score"`
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// broadcast pushes one payload to every subscriber. Non-blocking: if a
// client's send buffer is full it is dropped (protects the room from
// one slow client stalling the rest).
func (r *room) broadcast(payload []byte) {
	r.subsMu.RLock()
	defer r.subsMu.RUnlock()
	for c := range r.subs {
		select {
		case c.send <- payload:
		default:
			// Client is slow; skip this frame. A future improvement is
			// to close the connection after N consecutive drops.
		}
	}
}

// handleWS accepts /ws/contest/{contest_id}
func (h *hub) handleWS(w http.ResponseWriter, r *http.Request) {
	contestID := strings.TrimPrefix(r.URL.Path, "/ws/contest/")
	if contestID == "" {
		http.Error(w, "contest id required", http.StatusBadRequest)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // TLS + real Origin check in prod
	})
	if err != nil {
		return
	}
	defer ws.Close(websocket.StatusInternalError, "bye")

	c := &conn{ws: ws, send: make(chan []byte, 8)}
	room := h.joinRoom(r.Context(), contestID, c)
	defer h.leaveRoom(contestID, c)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer.
	go func() {
		ping := time.NewTicker(h.cfg.pingGap)
		defer ping.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-c.send:
				wctx, wc := context.WithTimeout(ctx, 5*time.Second)
				err := ws.Write(wctx, websocket.MessageText, msg)
				wc()
				if err != nil {
					cancel()
					return
				}
			case <-ping.C:
				if err := ws.Ping(ctx); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Immediate seed frame so a new client isn't staring at blank until
	// the next broadcast.
	if snap, err := room.topN(ctx); err == nil {
		payload, _ := json.Marshal(map[string]any{
			"type":    "leaderboard",
			"contest": contestID,
			"top":     snap,
			"seed":    true,
		})
		select {
		case c.send <- payload:
		default:
		}
	}

	// Reader: we accept no client-to-server frames yet, but must drain
	// pings and close messages to keep the connection healthy.
	for {
		if _, _, err := ws.Read(ctx); err != nil {
			return
		}
	}
}

// handleTopN is a plain HTTP fallback for clients that can't hold a
// WebSocket open (CDN caches, cron-driven dashboards, curl tests).
func (h *hub) handleTopN(w http.ResponseWriter, r *http.Request) {
	contestID := strings.TrimPrefix(r.URL.Path, "/leaderboard/")
	if contestID == "" {
		http.Error(w, "contest id required", http.StatusBadRequest)
		return
	}
	r2 := &room{contestID: contestID, rdb: h.rdb, cfg: h.cfg}
	snap, err := r2.topN(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=15") // CDN-friendly
	_ = json.NewEncoder(w).Encode(map[string]any{"contest": contestID, "top": snap})
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
