package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore implements the Store interface backed by Postgres with pgx.
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore creates a Postgres-backed ledger store.
func NewPgStore(ctx context.Context, dsn string) (*PgStore, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pool.Ping: %w", err)
	}
	return &PgStore{pool: pool}, nil
}

// Close releases the connection pool.
func (s *PgStore) Close() {
	s.pool.Close()
}

// Post writes all entries atomically at REPEATABLE READ isolation.
// If the idempotency key has already been used, returns ErrDuplicate.
func (s *PgStore) Post(ctx context.Context, txn Txn) error {
	if err := txn.Validate(); err != nil {
		return err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead,
	})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Dedup lives in a non-partitioned table because partitioned tables
	// require the partition key in every UNIQUE, which would defeat
	// idempotency (a fresh created_at makes every insert unique).
	// A 23505 here short-circuits before any journal rows are written.
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_idempotency (idempotency_key, txn_id)
		VALUES ($1, $1)
	`, txn.IdempotencyKey); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicate
		}
		return fmt.Errorf("insert idempotency: %w", err)
	}

	// Insert ledger entries. Each entry gets a unique (idempotency_key, entry_index).
	for i, e := range txn.Entries {
		_, err := tx.Exec(ctx, `
			INSERT INTO ledger_entries (
				txn_id, account_id, bucket, amount_cents, reason,
				ref_type, ref_id, idempotency_key, entry_index, created_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
		`, txn.IdempotencyKey, e.AccountID, e.Bucket, e.AmountCents, e.Reason,
			e.RefType, e.RefID, txn.IdempotencyKey, i)

		if err != nil {
			// Check for unique constraint violation on idempotency_key
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Constraint violation = duplicate idempotency key
				return ErrDuplicate
			}
			return fmt.Errorf("insert entry %d: %w", i, err)
		}
	}

	// Upsert balance cache: increment by amount_cents for each (account_id, bucket).
	for _, e := range txn.Entries {
		_, err := tx.Exec(ctx, `
			INSERT INTO balances (account_id, bucket, amount_cents, updated_at)
			VALUES ($1, $2, $3, now())
			ON CONFLICT (account_id, bucket)
			DO UPDATE SET
				amount_cents = balances.amount_cents + EXCLUDED.amount_cents,
				updated_at = now()
		`, e.AccountID, e.Bucket, e.AmountCents)
		if err != nil {
			return fmt.Errorf("upsert balance: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Balance returns one bucket's balance. If the cache row is missing,
// falls back to aggregating the journal (self-healing).
func (s *PgStore) Balance(ctx context.Context, acct AccountID, bucket Bucket) (int64, error) {
	var amt int64
	err := s.pool.QueryRow(ctx, `
		SELECT amount_cents FROM balances WHERE account_id = $1 AND bucket = $2
	`, acct, bucket).Scan(&amt)

	if err == nil {
		return amt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("query balance cache: %w", err)
	}

	// Cache miss: fall back to journal aggregation
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(amount_cents), 0)
		FROM ledger_entries
		WHERE account_id = $1 AND bucket = $2
	`, acct, bucket).Scan(&amt)
	if err != nil {
		return 0, fmt.Errorf("aggregate journal: %w", err)
	}
	return amt, nil
}

// Balances returns all buckets for an account. If the cache row is missing
// for a bucket, falls back to aggregating the journal.
func (s *PgStore) Balances(ctx context.Context, acct AccountID) (map[Bucket]int64, error) {
	out := make(map[Bucket]int64)

	// First, try cache
	rows, err := s.pool.Query(ctx, `
		SELECT bucket, amount_cents FROM balances WHERE account_id = $1
	`, acct)
	if err != nil {
		return nil, fmt.Errorf("query balance cache: %w", err)
	}
	defer rows.Close()

	cached := make(map[Bucket]bool)
	for rows.Next() {
		var bucket string
		var amt int64
		if err := rows.Scan(&bucket, &amt); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		out[Bucket(bucket)] = amt
		cached[Bucket(bucket)] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows.Err: %w", err)
	}

	// If all buckets were cached, we're done
	allBuckets := []Bucket{BucketDeposit, BucketWinning, BucketBonus, BucketHouse}
	allCached := true
	for _, b := range allBuckets {
		if !cached[b] {
			allCached = false
			break
		}
	}
	if allCached {
		return out, nil
	}

	// Cache miss for some buckets: fall back to journal aggregation
	rows2, err := s.pool.Query(ctx, `
		SELECT bucket, COALESCE(SUM(amount_cents), 0)
		FROM ledger_entries
		WHERE account_id = $1
		GROUP BY bucket
	`, acct)
	if err != nil {
		return nil, fmt.Errorf("aggregate journal: %w", err)
	}
	defer rows2.Close()

	for rows2.Next() {
		var bucket string
		var amt int64
		if err := rows2.Scan(&bucket, &amt); err != nil {
			return nil, fmt.Errorf("scan aggregate: %w", err)
		}
		// Only use journal value if bucket wasn't cached
		if !cached[Bucket(bucket)] {
			out[Bucket(bucket)] = amt
		}
	}
	if err := rows2.Err(); err != nil {
		return nil, fmt.Errorf("rows2.Err: %w", err)
	}

	return out, nil
}
