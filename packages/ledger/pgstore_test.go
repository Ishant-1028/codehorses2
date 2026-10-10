package ledger

import (
	"context"
	"errors"
	"os"
	"testing"
)

func pgStore(t *testing.T) *PgStore {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run")
	}
	ctx := context.Background()
	s, err := NewPgStore(ctx, dsn)
	if err != nil {
		t.Fatalf("NewPgStore: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func truncateTables(t *testing.T, s *PgStore) {
	t.Helper()
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, "TRUNCATE ledger_entries, balances CASCADE")
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func TestPgStore_PostAndBalance(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	// Grant 100 coins to user u1's deposit bucket
	txn := Txn{
		IdempotencyKey: "seed",
		Entries: []Entry{
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -100, Reason: ReasonBonus},
			{AccountID: "u1", Bucket: BucketDeposit, AmountCents: 100, Reason: ReasonBonus},
		},
	}
	if err := s.Post(ctx, txn); err != nil {
		t.Fatalf("Post: %v", err)
	}

	got, err := s.Balance(ctx, "u1", BucketDeposit)
	if err != nil || got != 100 {
		t.Fatalf("balance=%d err=%v, want 100", got, err)
	}
}

func TestPgStore_IdempotencyDedupes(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	txn := Txn{
		IdempotencyKey: "same-key",
		Entries: []Entry{
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -100, Reason: ReasonBonus},
			{AccountID: "u1", Bucket: BucketDeposit, AmountCents: 100, Reason: ReasonBonus},
		},
	}

	if err := s.Post(ctx, txn); err != nil {
		t.Fatal(err)
	}

	// Second post with same key should return ErrDuplicate
	err := s.Post(ctx, txn)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}

	// Balance should not double
	got, _ := s.Balance(ctx, "u1", BucketDeposit)
	if got != 100 {
		t.Fatalf("balance should not double: %d", got)
	}
}

func TestPgStore_EntryFeeTxn_PrioritySpend(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	// Seed user with bonus=30, deposit=50, winning=500
	seeds := []Txn{
		{
			IdempotencyKey: "seed-bonus",
			Entries: []Entry{
				{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -30, Reason: ReasonBonus},
				{AccountID: "user-a", Bucket: BucketBonus, AmountCents: 30, Reason: ReasonBonus},
			},
		},
		{
			IdempotencyKey: "seed-deposit",
			Entries: []Entry{
				{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -50, Reason: ReasonBonus},
				{AccountID: "user-a", Bucket: BucketDeposit, AmountCents: 50, Reason: ReasonBonus},
			},
		},
		{
			IdempotencyKey: "seed-winning",
			Entries: []Entry{
				{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -500, Reason: ReasonBonus},
				{AccountID: "user-a", Bucket: BucketWinning, AmountCents: 500, Reason: ReasonBonus},
			},
		},
	}
	for _, txn := range seeds {
		if err := s.Post(ctx, txn); err != nil {
			t.Fatal(err)
		}
	}

	// Create entry fee txn for 100 coins
	bals, err := s.Balances(ctx, "user-a")
	if err != nil {
		t.Fatal(err)
	}
	txn, err := EntryFeeTxn("entry-1", "user-a", "contest-1", 100, bals)
	if err != nil {
		t.Fatalf("EntryFeeTxn: %v", err)
	}
	if err := s.Post(ctx, txn); err != nil {
		t.Fatal(err)
	}

	// Verify priority spend: bonus -30, deposit -50, winning -20
	balsAfter, _ := s.Balances(ctx, "user-a")
	if balsAfter[BucketBonus] != 0 {
		t.Errorf("bonus: got %d, want 0", balsAfter[BucketBonus])
	}
	if balsAfter[BucketDeposit] != 0 {
		t.Errorf("deposit: got %d, want 0", balsAfter[BucketDeposit])
	}
	if balsAfter[BucketWinning] != 480 {
		t.Errorf("winning: got %d, want 480", balsAfter[BucketWinning])
	}
}

func TestPgStore_EntryFeeTxn_Insufficient(t *testing.T) {
	_, err := EntryFeeTxn("k", "u", "c", 100, map[Bucket]int64{
		BucketBonus: 10, BucketDeposit: 20, BucketWinning: 30,
	})
	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
}

func TestPgStore_PayoutTxn(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	// Seed house with 1000
	txn := Txn{
		IdempotencyKey: "seed-house",
		Entries: []Entry{
			{AccountID: "seed-acct", Bucket: BucketDeposit, AmountCents: -1000, Reason: ReasonDeposit},
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: 1000, Reason: ReasonDeposit},
		},
	}
	if err := s.Post(ctx, txn); err != nil {
		t.Fatal(err)
	}

	// Payout 200 to user
	payTxn, _ := PayoutTxn("payout-1", "user-a", "contest-1", 200)
	if err := s.Post(ctx, payTxn); err != nil {
		t.Fatal(err)
	}

	bal, _ := s.Balance(ctx, "user-a", BucketWinning)
	if bal != 200 {
		t.Errorf("user winning: got %d, want 200", bal)
	}
	houseBal, _ := s.Balance(ctx, HouseAccountID, BucketHouse)
	if houseBal != 800 {
		t.Errorf("house: got %d, want 800", houseBal)
	}
}

func TestPgStore_EndToEndContestFlow(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	// Seed two users with 1000 deposit each
	seeds := []Txn{
		{
			IdempotencyKey: "seed-a",
			Entries: []Entry{
				{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -1000, Reason: ReasonBonus},
				{AccountID: "a", Bucket: BucketDeposit, AmountCents: 1000, Reason: ReasonBonus},
			},
		},
		{
			IdempotencyKey: "seed-b",
			Entries: []Entry{
				{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -1000, Reason: ReasonBonus},
				{AccountID: "b", Bucket: BucketDeposit, AmountCents: 1000, Reason: ReasonBonus},
			},
		},
	}
	for _, txn := range seeds {
		if err := s.Post(ctx, txn); err != nil {
			t.Fatal(err)
		}
	}

	// Baseline: house holds -2000
	baseline, _ := s.Balance(ctx, HouseAccountID, BucketHouse)
	if baseline != -2000 {
		t.Fatalf("house baseline: %d, want -2000", baseline)
	}

	// Both enter 100-coin contest
	for _, u := range []AccountID{"a", "b"} {
		bals, _ := s.Balances(ctx, u)
		txn, err := EntryFeeTxn("entry-"+string(u), u, "c1", 100, bals)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Post(ctx, txn); err != nil {
			t.Fatal(err)
		}
	}

	// After entries: house = baseline + 200
	house, _ := s.Balance(ctx, HouseAccountID, BucketHouse)
	if house != baseline+200 {
		t.Fatalf("house after entries: %d, want %d", house, baseline+200)
	}

	// Settlement: 15% rake (30 coins), remainder to winner (170)
	rakeTxn, _ := RakeTxn("rake-c1", "c1", 30)
	if err := s.Post(ctx, rakeTxn); err != nil {
		t.Fatal(err)
	}
	payTxn, _ := PayoutTxn("payout-c1", "a", "c1", 170)
	if err := s.Post(ctx, payTxn); err != nil {
		t.Fatal(err)
	}

	// Verify final balances
	if bal, _ := s.Balance(ctx, "a", BucketDeposit); bal != 900 {
		t.Errorf("a deposit: %d, want 900", bal)
	}
	if bal, _ := s.Balance(ctx, "a", BucketWinning); bal != 170 {
		t.Errorf("a winning: %d, want 170", bal)
	}
	if bal, _ := s.Balance(ctx, "b", BucketDeposit); bal != 900 {
		t.Errorf("b deposit: %d, want 900", bal)
	}
	if bal, _ := s.Balance(ctx, "b", BucketWinning); bal != 0 {
		t.Errorf("b winning: %d, want 0", bal)
	}
}

func TestPgStore_Balances_CacheMiss_SelfHealing(t *testing.T) {
	s := pgStore(t)
	truncateTables(t, s)

	ctx := context.Background()

	// Post a transaction
	txn := Txn{
		IdempotencyKey: "seed",
		Entries: []Entry{
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: -100, Reason: ReasonBonus},
			{AccountID: "u1", Bucket: BucketDeposit, AmountCents: 100, Reason: ReasonBonus},
		},
	}
	if err := s.Post(ctx, txn); err != nil {
		t.Fatal(err)
	}

	// Manually corrupt the cache
	_, err := s.pool.Exec(ctx, "DELETE FROM balances WHERE account_id = 'u1'")
	if err != nil {
		t.Fatal(err)
	}

	// Balances() should self-heal from journal
	bals, err := s.Balances(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if bals[BucketDeposit] != 100 {
		t.Errorf("self-healing failed: got %d, want 100", bals[BucketDeposit])
	}
}
