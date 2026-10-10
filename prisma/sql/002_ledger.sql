-- Ledger tables: double-entry journal and balance cache
-- See packages/ledger/ledger.go for domain logic.

-- =====================================================================
-- ledger_entries — append-only journal (partitioned by month)
-- =====================================================================
CREATE TABLE IF NOT EXISTS ledger_entries (
  id               BIGSERIAL,
  txn_id           TEXT NOT NULL,
  account_id       TEXT NOT NULL,
  bucket           TEXT NOT NULL CHECK (bucket IN ('deposit', 'winning', 'bonus', 'house')),
  amount_cents     BIGINT NOT NULL,
  reason           TEXT NOT NULL CHECK (reason IN ('deposit', 'withdrawal', 'entry_fee', 'payout', 'rake', 'refund', 'bonus_grant')),
  ref_type         TEXT,
  ref_id           TEXT,
  idempotency_key  TEXT NOT NULL,
  entry_index      INTEGER NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT ledger_entries_pkey PRIMARY KEY (id, created_at),
  -- Partitioned tables require the partition key in every UNIQUE constraint.
  -- Idempotency is enforced globally by a separate UNIQUE INDEX on
  -- (idempotency_key, entry_index) covering all partitions -- Postgres
  -- lets us do that with a hash-safe global index below.
  CONSTRAINT ledger_entries_idem_uniq UNIQUE (idempotency_key, entry_index, created_at)
) PARTITION BY RANGE (created_at);

-- Create indexes on partitioned table
CREATE INDEX IF NOT EXISTS ledger_entries_account_bucket_idx
  ON ledger_entries (account_id, bucket, created_at DESC);
CREATE INDEX IF NOT EXISTS ledger_entries_txn_id_idx
  ON ledger_entries (txn_id);
CREATE INDEX IF NOT EXISTS ledger_entries_idempotency_key_idx
  ON ledger_entries (idempotency_key);

-- Create first 3 monthly partitions (current month + 2 ahead)
DO $$
DECLARE
  start_month timestamptz := date_trunc('month', now());
  i int;
  from_ts timestamptz;
  to_ts timestamptz;
  pname text;
BEGIN
  FOR i IN 0..2 LOOP
    from_ts := start_month + (i || ' months')::interval;
    to_ts := from_ts + interval '1 month';
    pname := format('ledger_entries_%s', to_char(from_ts, 'YYYYMM'));
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS %I PARTITION OF ledger_entries
         FOR VALUES FROM (%L) TO (%L)', pname, from_ts, to_ts);
  END LOOP;
END $$;

-- Default partition for dates outside explicit ranges
CREATE TABLE IF NOT EXISTS ledger_entries_default PARTITION OF ledger_entries DEFAULT;

-- =====================================================================
-- ledger_idempotency — global dedup for Post() calls
-- =====================================================================
-- The parent ledger_entries table is partitioned by created_at, and
-- Postgres requires every UNIQUE constraint on a partitioned table to
-- include the partition key. That makes it useless for dedup (each
-- insert has a fresh timestamp). This side table is not partitioned,
-- so its PK enforces global uniqueness across all history. Post()
-- inserts here first; a 23505 short-circuits with ErrDuplicate before
-- any journal rows are written.
CREATE TABLE IF NOT EXISTS ledger_idempotency (
  idempotency_key TEXT PRIMARY KEY,
  txn_id          TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- =====================================================================
-- balances — O(1) balance cache (upserted on every Post)
-- =====================================================================
CREATE TABLE IF NOT EXISTS balances (
  account_id   TEXT NOT NULL,
  bucket       TEXT NOT NULL CHECK (bucket IN ('deposit', 'winning', 'bonus', 'house')),
  amount_cents BIGINT NOT NULL DEFAULT 0,
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

  CONSTRAINT balances_pkey PRIMARY KEY (account_id, bucket)
);

CREATE INDEX IF NOT EXISTS balances_account_id_idx ON balances (account_id);

-- =====================================================================
-- Integrity views and functions
-- =====================================================================

-- View: unbalanced transactions (should always be empty)
CREATE OR REPLACE VIEW ledger_unbalanced_txns AS
  SELECT txn_id, SUM(amount_cents) AS sum_cents
    FROM ledger_entries
   GROUP BY txn_id
  HAVING SUM(amount_cents) <> 0;

-- Function: check txn balance (for nightly cron)
CREATE OR REPLACE FUNCTION ledger_check_txn_balance()
RETURNS TABLE(txn_id text, sum_cents bigint) AS $$
  SELECT txn_id, SUM(amount_cents)
    FROM ledger_entries
   GROUP BY txn_id
  HAVING SUM(amount_cents) <> 0;
$$ LANGUAGE sql STABLE;
