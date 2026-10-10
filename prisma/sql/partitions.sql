-- Partitioning strategy for CodeHorses.
--
-- Prisma cannot express PARTITION BY, so we apply these definitions
-- manually after `prisma migrate`. In production, run this as a one-shot
-- migration alongside the generated one.
--
-- The design goals are:
--   * ledger_entries is an append-only time series → RANGE partitions by
--     month. Old months can be moved to cheap storage or dropped after
--     seven years of retention.
--   * lineups sees an all-or-nothing burst per contest → HASH partitions
--     by contest_id keep one hot contest from monopolising the primary.

-- =====================================================================
-- 1) ledger_entries — monthly RANGE partitions
-- =====================================================================
-- Assumes the base table was created NON-partitioned. Below is the shape
-- you should aim for once you're ready to switch (offline, one-time).

/*
BEGIN;

ALTER TABLE "LedgerEntry" RENAME TO "LedgerEntry_old";

CREATE TABLE "LedgerEntry" (
  LIKE "LedgerEntry_old" INCLUDING ALL
) PARTITION BY RANGE ("createdAt");

CREATE TABLE "LedgerEntry_default" PARTITION OF "LedgerEntry" DEFAULT;

INSERT INTO "LedgerEntry" SELECT * FROM "LedgerEntry_old";
DROP TABLE "LedgerEntry_old";

COMMIT;
*/

-- Helper: create the next N months of partitions.
CREATE OR REPLACE FUNCTION ledger_ensure_partitions(months_ahead int)
RETURNS void AS $$
DECLARE
  start_month timestamptz := date_trunc('month', now());
  i int;
  from_ts timestamptz;
  to_ts timestamptz;
  pname text;
BEGIN
  FOR i IN 0..months_ahead LOOP
    from_ts := start_month + (i || ' months')::interval;
    to_ts := from_ts + interval '1 month';
    pname := format('LedgerEntry_%s', to_char(from_ts, 'YYYYMM'));
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS %I PARTITION OF "LedgerEntry"
         FOR VALUES FROM (%L) TO (%L)', pname, from_ts, to_ts);
  END LOOP;
END;
$$ LANGUAGE plpgsql;

-- =====================================================================
-- 2) lineups — HASH by contest_id
-- =====================================================================
/*
BEGIN;

ALTER TABLE "Lineup" RENAME TO "Lineup_old";

CREATE TABLE "Lineup" (
  LIKE "Lineup_old" INCLUDING ALL
) PARTITION BY HASH ("contestId");

DO $$
BEGIN
  FOR i IN 0..15 LOOP
    EXECUTE format(
      'CREATE TABLE IF NOT EXISTS %I PARTITION OF "Lineup"
         FOR VALUES WITH (MODULUS 16, REMAINDER %s)',
      format('Lineup_p%s', i), i);
  END LOOP;
END $$;

INSERT INTO "Lineup" SELECT * FROM "Lineup_old";
DROP TABLE "Lineup_old";

COMMIT;
*/

-- =====================================================================
-- 3) Ledger integrity check — runs cheaply, page if drift
-- =====================================================================
-- Every txn MUST sum to zero. Run this as a nightly cron:
--
--   SELECT * FROM ledger_check_txn_balance();
--
-- Any rows returned = accounting drift. Page on-call.

CREATE OR REPLACE FUNCTION ledger_check_txn_balance()
RETURNS TABLE(txn_id text, sum_cents bigint) AS $$
  SELECT "txnId", SUM("amountCents")
    FROM "LedgerEntry"
   GROUP BY "txnId"
  HAVING SUM("amountCents") <> 0;
$$ LANGUAGE sql STABLE;
