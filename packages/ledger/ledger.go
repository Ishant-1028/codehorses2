// Package ledger implements a double-entry, three-bucket wallet.
//
// Every state change goes through Post, which writes N ledger entries
// whose amounts sum to zero. Balances are derived from the journal;
// balances_cache is a fast-read materialisation. All state-changing
// operations require an idempotency key so retries are safe.
//
// Buckets:
//   - deposit  — money the user put in (real-money: withdrawable only
//     after play-through)
//   - winning  — money the user won (real-money: freely withdrawable)
//   - bonus    — promo / referral / signup grants (never withdrawable)
//   - house    — the platform's own account (rake, promo cost, overlay)
//
// At MVP play-money mode, "deposit" and "winning" behave the same;
// the split matters only when a real payment provider is wired in.
package ledger

import (
	"context"
	"errors"
	"fmt"
)

// Bucket identifies which sub-balance an entry moves.
type Bucket string

const (
	BucketDeposit Bucket = "deposit"
	BucketWinning Bucket = "winning"
	BucketBonus   Bucket = "bonus"
	BucketHouse   Bucket = "house"
)

// Reason describes why an entry was posted. Free-form for now but stable
// enough that reports can group on it.
type Reason string

const (
	ReasonDeposit    Reason = "deposit"
	ReasonWithdrawal Reason = "withdrawal"
	ReasonEntryFee   Reason = "entry_fee"
	ReasonPayout     Reason = "payout"
	ReasonRake       Reason = "rake"
	ReasonRefund     Reason = "refund"
	ReasonBonus      Reason = "bonus_grant"
)

// AccountID uniquely identifies a user or the house account.
type AccountID string

// HouseAccountID is the pseudo-account that holds rake and issues promo bonuses.
const HouseAccountID AccountID = "house"

// Entry is one row of the double-entry journal.
type Entry struct {
	AccountID AccountID
	Bucket    Bucket
	// AmountCents is signed. Sum of all entries in a Txn MUST be zero.
	AmountCents int64
	Reason      Reason
	RefType     string
	RefID       string
}

// Txn is the atomic unit of the ledger — a single Post writes all of a
// Txn's entries or none of them.
type Txn struct {
	IdempotencyKey string
	Entries        []Entry
}

// Store persists journal entries and lets us query balances. Concrete
// implementations live in packages/store.
type Store interface {
	// Post writes all entries of a Txn atomically. If the idempotency key
	// has already been used, Post is a no-op and returns ErrDuplicate.
	Post(ctx context.Context, txn Txn) error
	// Balance returns the account's current balance in one bucket.
	Balance(ctx context.Context, acct AccountID, bucket Bucket) (int64, error)
	// Balances returns all buckets for one account.
	Balances(ctx context.Context, acct AccountID) (map[Bucket]int64, error)
}

// ErrDuplicate is returned by Post when the idempotency key was already used.
var ErrDuplicate = errors.New("ledger: duplicate txn")

// ErrInsufficient is returned when a user does not have enough coins to
// cover a debit across the priority order.
var ErrInsufficient = errors.New("ledger: insufficient balance")

// Validate checks a Txn is internally consistent (sums to zero, idempotency
// key set, at least two entries). Callers should Validate before Post.
func (t Txn) Validate() error {
	if t.IdempotencyKey == "" {
		return fmt.Errorf("ledger: idempotency key required")
	}
	if len(t.Entries) < 2 {
		return fmt.Errorf("ledger: txn needs >=2 entries, got %d", len(t.Entries))
	}
	var sum int64
	for _, e := range t.Entries {
		sum += e.AmountCents
	}
	if sum != 0 {
		return fmt.Errorf("ledger: txn does not balance: sum=%d", sum)
	}
	return nil
}

// EntryFeeTxn produces a canonical entry-fee transaction: debit the user
// across the buckets in priority order (bonus → deposit → winning) and
// credit the house. Callers pass current bucket balances so the split
// can be computed without a round-trip.
//
// Priority is Dream11-standard and matters for real-money: bonus and
// deposit must be "played through" before they become withdrawable, so
// they are spent first.
func EntryFeeTxn(
	idempotencyKey string,
	userID AccountID,
	contestID string,
	entryFeeCents int64,
	current map[Bucket]int64,
) (Txn, error) {
	if entryFeeCents <= 0 {
		return Txn{}, fmt.Errorf("ledger: non-positive entry fee")
	}
	priority := []Bucket{BucketBonus, BucketDeposit, BucketWinning}
	remaining := entryFeeCents
	entries := make([]Entry, 0, len(priority)+1)
	for _, b := range priority {
		if remaining == 0 {
			break
		}
		have := current[b]
		if have <= 0 {
			continue
		}
		use := have
		if use > remaining {
			use = remaining
		}
		entries = append(entries, Entry{
			AccountID:   userID,
			Bucket:      b,
			AmountCents: -use,
			Reason:      ReasonEntryFee,
			RefType:     "contest",
			RefID:       contestID,
		})
		remaining -= use
	}
	if remaining > 0 {
		return Txn{}, ErrInsufficient
	}
	// Balancing credit to the house (held until settlement / rake).
	entries = append(entries, Entry{
		AccountID:   HouseAccountID,
		Bucket:      BucketHouse,
		AmountCents: entryFeeCents,
		Reason:      ReasonEntryFee,
		RefType:     "contest",
		RefID:       contestID,
	})
	txn := Txn{IdempotencyKey: idempotencyKey, Entries: entries}
	if err := txn.Validate(); err != nil {
		return Txn{}, err
	}
	return txn, nil
}

// PayoutTxn credits a user's winning bucket and debits the house. Rake is
// handled by RakeTxn separately so the two are auditable.
func PayoutTxn(idempotencyKey string, userID AccountID, contestID string, amountCents int64) (Txn, error) {
	if amountCents <= 0 {
		return Txn{}, fmt.Errorf("ledger: non-positive payout")
	}
	txn := Txn{
		IdempotencyKey: idempotencyKey,
		Entries: []Entry{
			{
				AccountID:   HouseAccountID,
				Bucket:      BucketHouse,
				AmountCents: -amountCents,
				Reason:      ReasonPayout,
				RefType:     "contest",
				RefID:       contestID,
			},
			{
				AccountID:   userID,
				Bucket:      BucketWinning,
				AmountCents: amountCents,
				Reason:      ReasonPayout,
				RefType:     "contest",
				RefID:       contestID,
			},
		},
	}
	return txn, txn.Validate()
}

// RakeTxn moves the platform's cut out of the contest's collected pool
// into a permanent house-revenue sub-account. Modelled as house→house so
// it doesn't affect user balances.
func RakeTxn(idempotencyKey string, contestID string, amountCents int64) (Txn, error) {
	if amountCents <= 0 {
		return Txn{}, fmt.Errorf("ledger: non-positive rake")
	}
	txn := Txn{
		IdempotencyKey: idempotencyKey,
		Entries: []Entry{
			{
				AccountID:   HouseAccountID,
				Bucket:      BucketHouse,
				AmountCents: -amountCents,
				Reason:      ReasonRake,
				RefType:     "contest",
				RefID:       contestID,
			},
			{
				AccountID:   HouseAccountID,
				Bucket:      BucketHouse,
				AmountCents: amountCents,
				Reason:      ReasonRake,
				RefType:     "contest_rake",
				RefID:       contestID,
			},
		},
	}
	return txn, txn.Validate()
}

// BonusTxn grants promo coins from the house to a user's bonus bucket.
func BonusTxn(idempotencyKey string, userID AccountID, refType, refID string, amountCents int64) (Txn, error) {
	if amountCents <= 0 {
		return Txn{}, fmt.Errorf("ledger: non-positive bonus")
	}
	txn := Txn{
		IdempotencyKey: idempotencyKey,
		Entries: []Entry{
			{
				AccountID:   HouseAccountID,
				Bucket:      BucketHouse,
				AmountCents: -amountCents,
				Reason:      ReasonBonus,
				RefType:     refType,
				RefID:       refID,
			},
			{
				AccountID:   userID,
				Bucket:      BucketBonus,
				AmountCents: amountCents,
				Reason:      ReasonBonus,
				RefType:     refType,
				RefID:       refID,
			},
		},
	}
	return txn, txn.Validate()
}
