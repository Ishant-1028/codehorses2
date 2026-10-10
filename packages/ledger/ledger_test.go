package ledger

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestEntryFeeTxn_PrioritySpend(t *testing.T) {
	txn, err := EntryFeeTxn(
		"idem-1",
		"user-a",
		"contest-1",
		100,
		map[Bucket]int64{BucketBonus: 30, BucketDeposit: 50, BucketWinning: 500},
	)
	if err != nil {
		t.Fatalf("EntryFeeTxn: %v", err)
	}
	got := map[Bucket]int64{}
	for _, e := range txn.Entries {
		if e.AccountID == "user-a" {
			got[e.Bucket] = e.AmountCents
		}
	}
	if got[BucketBonus] != -30 || got[BucketDeposit] != -50 || got[BucketWinning] != -20 {
		t.Fatalf("unexpected debit split: %+v", got)
	}
}

func TestEntryFeeTxn_Insufficient(t *testing.T) {
	_, err := EntryFeeTxn("k", "u", "c", 100, map[Bucket]int64{
		BucketBonus: 10, BucketDeposit: 20, BucketWinning: 30,
	})
	if !errors.Is(err, ErrInsufficient) {
		t.Fatalf("expected ErrInsufficient, got %v", err)
	}
}

func TestTxn_MustBalance(t *testing.T) {
	txn := Txn{
		IdempotencyKey: "k",
		Entries: []Entry{
			{AccountID: "u", Bucket: BucketDeposit, AmountCents: -50, Reason: ReasonEntryFee},
			{AccountID: HouseAccountID, Bucket: BucketHouse, AmountCents: 49, Reason: ReasonEntryFee},
		},
	}
	if err := txn.Validate(); err == nil {
		t.Fatal("expected unbalanced-txn error")
	}
}

func TestMemStore_PostAndBalance(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	if err := m.Grant(ctx, "seed", "u1", BucketDeposit, 100); err != nil {
		t.Fatalf("grant: %v", err)
	}
	got, err := m.Balance(ctx, "u1", BucketDeposit)
	if err != nil || got != 100 {
		t.Fatalf("balance=%d err=%v", got, err)
	}
}

func TestMemStore_IdempotencyDedupes(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	if err := m.Grant(ctx, "same-key", "u1", BucketDeposit, 100); err != nil {
		t.Fatal(err)
	}
	err := m.Grant(ctx, "same-key", "u1", BucketDeposit, 100)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
	got, _ := m.Balance(ctx, "u1", BucketDeposit)
	if got != 100 {
		t.Fatalf("balance should not double: %d", got)
	}
}

func TestMemStore_ConcurrentUniqueIdempotencyKeys(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	const N = 500

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = m.Grant(ctx, keyN(i), "u1", BucketWinning, 1)
		}(i)
	}
	wg.Wait()

	got, _ := m.Balance(ctx, "u1", BucketWinning)
	if got != N {
		t.Fatalf("expected %d, got %d", N, got)
	}
}

func TestEndToEndContestFlow(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()

	// Two users each seeded with 1000 deposit coins.
	if err := m.Grant(ctx, "seed-a", "a", BucketDeposit, 1000); err != nil {
		t.Fatal(err)
	}
	if err := m.Grant(ctx, "seed-b", "b", BucketDeposit, 1000); err != nil {
		t.Fatal(err)
	}

	// Baseline: house holds -2000 (bonuses granted to users are house cost).
	baseline, _ := m.Balance(ctx, HouseAccountID, BucketHouse)
	if baseline != -2000 {
		t.Fatalf("house baseline after seeding: %d, want -2000", baseline)
	}

	// Both enter a 100-coin contest.
	for _, u := range []AccountID{"a", "b"} {
		bals, _ := m.Balances(ctx, u)
		txn, err := EntryFeeTxn("entry-"+string(u), u, "c1", 100, bals)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.Post(ctx, txn); err != nil {
			t.Fatal(err)
		}
	}

	// After entries: house = baseline + 200 (pool held pending settlement).
	house, _ := m.Balance(ctx, HouseAccountID, BucketHouse)
	if house != baseline+200 {
		t.Fatalf("house after entries: %d, want %d", house, baseline+200)
	}

	// Settlement: 15% rake, remainder to winner (user a).
	rakeTxn, _ := RakeTxn("rake-c1", "c1", 30)
	if err := m.Post(ctx, rakeTxn); err != nil {
		t.Fatal(err)
	}
	payTxn, _ := PayoutTxn("payout-c1", "a", "c1", 170)
	if err := m.Post(ctx, payTxn); err != nil {
		t.Fatal(err)
	}

	if bal, _ := m.Balance(ctx, "a", BucketDeposit); bal != 900 {
		t.Fatalf("a deposit: %d", bal)
	}
	if bal, _ := m.Balance(ctx, "a", BucketWinning); bal != 170 {
		t.Fatalf("a winnings: %d", bal)
	}
	if bal, _ := m.Balance(ctx, "b", BucketDeposit); bal != 900 {
		t.Fatalf("b deposit: %d", bal)
	}
	if bal, _ := m.Balance(ctx, "b", BucketWinning); bal != 0 {
		t.Fatalf("b winnings: %d", bal)
	}
}

func keyN(i int) string {
	return "k-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
