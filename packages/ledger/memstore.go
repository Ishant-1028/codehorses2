package ledger

import (
	"context"
	"sync"
)

// MemStore is an in-memory ledger used for tests and local dev. It is
// concurrent-safe and enforces idempotency.
type MemStore struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	balances map[AccountID]map[Bucket]int64
}

// NewMemStore creates an empty in-memory ledger.
func NewMemStore() *MemStore {
	return &MemStore{
		seen:     make(map[string]struct{}),
		balances: make(map[AccountID]map[Bucket]int64),
	}
}

// Post writes all entries atomically. Returns ErrDuplicate if the idempotency
// key was already used.
func (m *MemStore) Post(ctx context.Context, txn Txn) error {
	if err := txn.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, dup := m.seen[txn.IdempotencyKey]; dup {
		return ErrDuplicate
	}
	m.seen[txn.IdempotencyKey] = struct{}{}
	for _, e := range txn.Entries {
		if m.balances[e.AccountID] == nil {
			m.balances[e.AccountID] = make(map[Bucket]int64)
		}
		m.balances[e.AccountID][e.Bucket] += e.AmountCents
	}
	return nil
}

// Balance returns one bucket's balance.
func (m *MemStore) Balance(ctx context.Context, acct AccountID, bucket Bucket) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[acct][bucket], nil
}

// Balances returns all buckets for an account.
func (m *MemStore) Balances(ctx context.Context, acct AccountID) (map[Bucket]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[Bucket]int64, 3)
	for k, v := range m.balances[acct] {
		out[k] = v
	}
	return out, nil
}

// Grant is a convenience helper for tests: post an initial credit to a
// user's specified bucket, balanced against the house.
func (m *MemStore) Grant(ctx context.Context, key string, acct AccountID, bucket Bucket, cents int64) error {
	txn := Txn{
		IdempotencyKey: key,
		Entries: []Entry{
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -cents, Reason: ReasonBonus},
			{AccountID: acct, Bucket: bucket, AmountCents: cents, Reason: ReasonBonus},
		},
	}
	return m.Post(ctx, txn)
}
