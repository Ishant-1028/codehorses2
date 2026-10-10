// Command api is the stateless HTTP layer.
//
// Responsibilities:
//   - Wallet reads (balance from cache, top up in play-money mode)
//   - Lineup CRUD (create, update, lock at contest start)
//   - Contest listings and metadata
//   - Real-money hooks (payment, KYC, geo) — behind interfaces with
//     mock implementations at MVP.
//
// Deliberately does NOT talk to the CF API — that's ingest's sole job.
// Deliberately does NOT push to WebSockets — that's gateway's job.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/g1tsg/codehorses/packages/ledger"
	scoringrules "github.com/g1tsg/codehorses/packages/scoringrules"
	"github.com/g1tsg/codehorses/packages/store"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
)

type config struct {
	listenAddr string
	redisAddr  string
	pgDSN      string
}

func loadConfig() config {
	return config{
		listenAddr: getenv("LISTEN_ADDR", ":8081"),
		redisAddr:  getenv("REDIS_ADDR", "localhost:6379"),
		// Empty DSN falls back to the in-memory ledger — that's the
		// play-money MVP default. Set POSTGRES_DSN once you've applied
		// prisma/sql/002_ledger.sql.
		pgDSN: os.Getenv("POSTGRES_DSN"),
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
		slog.Warn("redis unreachable — leaderboard endpoints will 503", "err", err)
	}
	defer rdb.Close()

	// Wallet: Postgres store when POSTGRES_DSN is set, MemStore otherwise.
	// Both satisfy the ledger.Store interface — handlers don't care which.
	var walletLedger ledger.Store
	var db *sql.DB
	if cfg.pgDSN != "" {
		pg, err := ledger.NewPgStore(ctx, cfg.pgDSN)
		if err != nil {
			slog.Error("postgres ledger init failed", "err", err)
			os.Exit(1)
		}
		defer pg.Close()
		walletLedger = pg
		slog.Info("wallet ledger", "backend", "postgres")

		// Open a separate DB connection for auth queries.
		db, err = sql.Open("postgres", cfg.pgDSN)
		if err != nil {
			slog.Error("postgres db init failed", "err", err)
			os.Exit(1)
		}
		if err := db.PingContext(ctx); err != nil {
			slog.Error("postgres db ping failed", "err", err)
			os.Exit(1)
		}
		defer db.Close()
		slog.Info("auth database", "backend", "postgres")
	} else {
		walletLedger = ledger.NewMemStore()
		slog.Info("wallet ledger", "backend", "memory (play-money MVP)")
		slog.Info("auth database", "backend", "none (set POSTGRES_DSN for auth)")
	}

	srv := &server{
		rdb:      rdb,
		ledger:   walletLedger,
		payments: &mockPayments{ledger: walletLedger},
		kyc:      alwaysPassKYC{},
		geo:      alwaysAllowGeo{},
		db:       db,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("/v1/rules", srv.getRules)
	mux.HandleFunc("/v1/wallet/", srv.wallet)
	mux.HandleFunc("/v1/deposit/mock", srv.mockDeposit)
	mux.HandleFunc("/v1/leaderboard/", srv.leaderboard)
	mux.HandleFunc("/v1/auth/signup", srv.signup)
	mux.HandleFunc("/v1/auth/login", srv.login)
	mux.HandleFunc("/v1/auth/logout", srv.logout)

	httpSrv := &http.Server{Addr: cfg.listenAddr, Handler: withLogging(mux), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	slog.Info("api listening", "addr", cfg.listenAddr)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server exited", "err", err)
		os.Exit(1)
	}
}

// server holds all the collaborators the HTTP handlers need. Interfaces
// let the play-money MVP swap in real implementations later without
// touching any handler code.
type server struct {
	rdb      *redis.Client
	ledger   ledger.Store
	payments PaymentProvider
	kyc      KycVerifier
	geo      GeoResolver
	db       *sql.DB        // Postgres connection for auth queries
	sessions sync.Map       // sessionToken (string) -> userID (string)
}

// ------ Real-money-ready interfaces ------

type PaymentProvider interface {
	// StartDeposit reserves the funds; funds are only credited on webhook.
	StartDeposit(ctx context.Context, userID string, cents int64, idem string) (string, error)
}

type KycVerifier interface {
	Status(ctx context.Context, userID string) (string, error)
}

type GeoResolver interface {
	Allowed(ctx context.Context, ip string) (bool, error)
}

// mockPayments credits the user's deposit bucket instantly. Real
// providers post via webhook.
type mockPayments struct{ ledger ledger.Store }

func (m *mockPayments) StartDeposit(ctx context.Context, userID string, cents int64, idem string) (string, error) {
	txn := ledger.Txn{
		IdempotencyKey: idem,
		Entries: []ledger.Entry{
			{
				AccountID: ledger.HouseAccountID, Bucket: ledger.BucketHouse,
				AmountCents: -cents, Reason: ledger.ReasonDeposit,
			},
			{
				AccountID: ledger.AccountID(userID), Bucket: ledger.BucketDeposit,
				AmountCents: cents, Reason: ledger.ReasonDeposit,
			},
		},
	}
	if err := m.ledger.Post(ctx, txn); err != nil {
		return "", err
	}
	return "mock-txn-" + idem, nil
}

type alwaysPassKYC struct{}

func (alwaysPassKYC) Status(ctx context.Context, userID string) (string, error) { return "verified", nil }

type alwaysAllowGeo struct{}

func (alwaysAllowGeo) Allowed(ctx context.Context, ip string) (bool, error) { return true, nil }

// ------ Handlers ------

func (s *server) getRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"scoring_version": int(scoringrules.Latest),
		"team_size":       scoringrules.TeamSize,
		"salary_cap":      scoringrules.SalaryCap,
		"max_per_country": scoringrules.MaxPerCountry,
	})
}

// GET /v1/wallet/{userId}
func (s *server) wallet(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimPrefix(r.URL.Path, "/v1/wallet/")
	if userID == "" {
		http.Error(w, "user id required", http.StatusBadRequest)
		return
	}
	bals, err := s.ledger.Balances(r.Context(), ledger.AccountID(userID))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":  userID,
		"deposit":  bals[ledger.BucketDeposit],
		"winning":  bals[ledger.BucketWinning],
		"bonus":    bals[ledger.BucketBonus],
		"currency": "PLAY_COIN",
	})
}

// POST /v1/deposit/mock
// Body: {"user_id":"...","cents":10000,"idempotency_key":"..."}
func (s *server) mockDeposit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		UserID         string `json:"user_id"`
		Cents          int64  `json:"cents"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.UserID == "" || body.Cents <= 0 || body.IdempotencyKey == "" {
		http.Error(w, "user_id, cents>0 and idempotency_key required", http.StatusBadRequest)
		return
	}
	ref, err := s.payments.StartDeposit(r.Context(), body.UserID, body.Cents, body.IdempotencyKey)
	if err != nil {
		if errors.Is(err, ledger.ErrDuplicate) {
			writeJSON(w, http.StatusOK, map[string]any{"deduped": true})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"txn_ref": ref})
}

// GET /v1/leaderboard/{contest_id}?top=100
func (s *server) leaderboard(w http.ResponseWriter, r *http.Request) {
	contestID := strings.TrimPrefix(r.URL.Path, "/v1/leaderboard/")
	if contestID == "" {
		http.Error(w, "contest id required", http.StatusBadRequest)
		return
	}
	top := int64(50)
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 && n <= 500 {
			top = n
		}
	}
	rows, err := s.rdb.ZRevRangeWithScores(r.Context(), store.LeaderboardKey(contestID), 0, top-1).Result()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for i, row := range rows {
		out = append(out, map[string]any{
			"rank":   i + 1,
			"handle": row.Member,
			"score":  row.Score,
		})
	}
	w.Header().Set("Cache-Control", "public, max-age=10")
	writeJSON(w, http.StatusOK, map[string]any{"contest": contestID, "top": out})
}

// POST /v1/auth/signup
// Body: {"email":"...","password":"..."}
// Assumes users table has (id, email, password_hash, created_at) columns.
func (s *server) signup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "auth not available (no database)", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Email == "" || body.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	// Hash password with bcrypt (cost 10).
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), 10)
	if err != nil {
		slog.Error("bcrypt hash failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Insert user into database. Prisma uses camelCase column names.
	// Use cuid-style ID generation for consistency with Prisma.
	userID := generateCUID()
	_, err = s.db.ExecContext(r.Context(),
		`INSERT INTO "User" (id, email, "passwordHash", "createdAt") VALUES ($1, $2, $3, NOW())`,
		userID, body.Email, string(hash))
	if err != nil {
		// Check for duplicate email (unique constraint violation).
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			http.Error(w, "email already exists", http.StatusConflict)
			return
		}
		slog.Error("user insert failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID})
}

// POST /v1/auth/login
// Body: {"email":"...","password":"..."}
// Returns: {"user_id":"...","session_token":"..."}
func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.db == nil {
		http.Error(w, "auth not available (no database)", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Email == "" || body.Password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}

	// Look up user by email. Prisma uses camelCase column names.
	var userID, passwordHash string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id, "passwordHash" FROM "User" WHERE email = $1`,
		body.Email).Scan(&userID, &passwordHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "invalid email or password", http.StatusUnauthorized)
			return
		}
		slog.Error("user lookup failed", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Verify password with bcrypt.
	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(body.Password)); err != nil {
		http.Error(w, "invalid email or password", http.StatusUnauthorized)
		return
	}

	// Generate session token and store in sessions map.
	sessionToken := uuid.NewString()
	s.sessions.Store(sessionToken, userID)

	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":       userID,
		"session_token": sessionToken,
	})
}

// POST /v1/auth/logout
// Body: {"session_token":"..."}
func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionToken string `json:"session_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.SessionToken == "" {
		http.Error(w, "session_token required", http.StatusBadRequest)
		return
	}

	// Remove from sessions map.
	s.sessions.Delete(body.SessionToken)

	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

// ------ helpers ------

func generateCUID() string {
	// Simple CUID-like generator for MVP. Production should use a proper
	// CUID library (github.com/lucsky/cuid) for collision resistance.
	// Format: c + base36-encoded timestamp + random suffix.
	return "c" + uuid.NewString()[:24]
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur_ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
