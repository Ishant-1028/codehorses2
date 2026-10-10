# CodeHorses — System Architecture

Fantasy platform for competitive programming. Users draft a "team" of Codeforces
programmers before a round, points accrue live from real contest performance,
prize pool splits by lineup score.

This document describes the target architecture: what the MVP ships, and how
each component scales linearly up to 1M concurrent users at contest start.

---

## Design principles

1. **The event log is the source of truth.** Codeforces submissions become
   internal events; scoring is a pure function of the event log. Any scoring
   bug is fixable by replaying the log.
2. **Single writer per external limit.** Codeforces caps the world at
   0.5 req/sec. One ingest worker owns that budget for the entire product —
   never fan out CF calls per-user.
3. **Broadcast, not fan-out.** Leaderboards are per-contest, not per-user.
   Cost of pushing an update is O(contests active), not O(users watching).
4. **Money moves through a double-entry ledger.** Balances are derived from
   an append-only journal. Reconciliation runs continuously.
5. **Real-money hooks stubbed from day 1.** Wallet, KYC, geo, payments live
   behind interfaces with mock implementations. Real launch = swap the impl.

---

## Component map

```
                   ┌────────────────────────────────────────┐
                   │ Codeforces API (external)              │
                   │ 0.5 req/sec global, poll-only          │
                   └───────────────┬────────────────────────┘
                                   │  batched: up to 10k handles / call
                        ┌──────────▼──────────┐
                        │ services/ingest     │  (Go, 1 instance per contest)
                        │ - rate governor     │
                        │ - normalise events  │──── writes ──► Redis Streams
                        │ - poll cadence      │                (event log)
                        └──────────┬──────────┘
                                   │
              ┌────────────────────┼────────────────────┐
              │                    │                    │
     ┌────────▼────────┐  ┌────────▼────────┐  ┌────────▼────────┐
     │ scoring #1      │  │ scoring #2      │  │ scoring #N      │
     │ (Go, horizontal)│  │                 │  │                 │
     │ - consumer group│  │                 │  │                 │
     │ - pure fn(rules)│  │                 │  │                 │
     └────────┬────────┘  └────────┬────────┘  └────────┬────────┘
              │                    │                    │
              └──────────────►  Redis ZSET  ◄────────────┘
                              leaderboard:<cid>
                              (canonical live state)
                                     │
                                     │ pub/sub: cid.leaderboard
                                     ▼
                        ┌──────────────────────┐
                        │ services/gateway     │  (Go, N instances)
                        │ - WebSocket / SSE    │  sticky-routed by cid
                        │ - per-contest fan-out│
                        │ - 500ms debounce     │
                        └──────────┬───────────┘
                                   │
                     ┌─────────────▼──────────────┐
                     │ services/api               │  (Go, stateless)
                     │ - REST: auth, lineup,      │
                     │   wallet, contests, admin  │
                     │ - idempotency layer        │
                     │ - real-money hooks (stub)  │
                     └──────┬─────────────┬───────┘
                            │             │
                    ┌───────▼───┐    ┌────▼──────┐
                    │ Postgres  │    │ Redis     │
                    │ - users   │    │ - cache   │
                    │ - lineups │    │ - locks   │
                    │ - LEDGER  │    │ - pubsub  │
                    │ - contests│    │ - streams │
                    │ - audit   │    │           │
                    └───────────┘    └───────────┘
                                     
                     Next.js frontend  (SSR + WS client)
```

---

## Scaling math — 100k → 1M concurrent users

Anchor point: **IPL final on Dream11 in 2024 had ~11M concurrent users**
[[public reporting]]. Their infra is a proof of existence for the shape below.
Our target of 1M is the same shape at 1/10 the scale.

### Fan-in: Codeforces API

| Metric | Value | Notes |
|---|---|---|
| CF rate limit | 0.5 req/sec | Fixed by them |
| Handles per authenticated `contest.standings` call | 10,000 | Fixed by them |
| Distinct handles across 1M lineups (6 each) | ~50k unique programmers max | Long-tail overlap huge — top ~2000 handles cover 95%+ |
| Poll cadence needed for "live-feeling" | 3–5 seconds | Achievable with 1–2 calls per poll |

**Verdict: unchanged by user count.** The API cost is O(distinct handles used),
not O(users). One ingest worker suffices for 1M users.

### Fan-out: live leaderboard

| Users watching contest X | Naïve (per-user query) | Broadcast (our design) |
|---|---|---|
| 1k | 200 QPS | 0.3 msgs/sec (one broadcast per poll) |
| 100k | 20k QPS 💀 | 0.3 msgs/sec |
| 1M | 200k QPS 💀 | 0.3 msgs/sec |

Cost is invariant to user count. WebSocket gateway just forwards bytes from
Redis pubsub to N subscribers.

### WebSocket gateway sharding

Single tuned Go node handles ~500k WS connections (`GOMAXPROCS`, `ulimit -n`,
`SO_REUSEPORT`, kernel `somaxconn`). For 1M concurrent:

- 3 gateway pods (2 for load, 1 headroom)
- LB sticky-routes by `contest_id` — all viewers of one contest land on the
  same pod, single Redis subscription serves them all
- If contest X has 800k viewers, spin a 4th pod, LB rebalances

Sharding key: `contest_id`. This is a natural partition — one hot contest per
day, most contests have <10k viewers.

### Postgres: contest-start write burst

Worst case: 1M users all enter one contest in the final 5 minutes.

```
1,000,000 entries / 300 sec = 3,333 writes/sec
```

Each entry is one transaction: `INSERT lineup, INSERT ledger_entry×2,
UPDATE balance_cache`. Postgres 16 on m6i.2xlarge handles 10k+ TPS with
default tuning [[benchmarks]]. Comfortable.

For 10M-scale (Dream11 IPL final territory):
- Partition `lineups` by `contest_id` (each contest is its own hot partition,
  cold data doesn't compete)
- Partition `ledger_entries` monthly (append-only, natural time series)
- Read replica for `GET /wallet/balance` (99% of read traffic)
- pgBouncer transaction mode in front

### Redis: leaderboard hot key

One ZSET per contest. With 1M members and ~10 writes/sec during peak:

- Memory: ~150 MB per contest (16 bytes/member × 1M members × ZSET overhead)
- Sharding: Redis Cluster, slot chosen by `contest_id` hash
- Read: `ZREVRANGE 0 99` returns top-100 in ~200µs even at 1M members

### Bandwidth

Live leaderboard broadcast, top-100 diff, 500ms debounce:
- Payload: ~2 KB per broadcast
- Rate: 2 msgs/sec × 2 KB = 4 KB/sec per subscriber
- 1M subscribers = 4 GB/sec egress

That's a real number. Mitigation:
- Push only diffs (position changes), not full top-100 each tick
- Client-side interpolation for smooth UI without server frequency
- CDN for static leaderboard "snapshots" every 30s, WS only for deltas
- Regional gateway nodes (Fly.io deploys to N regions natively)

---

## Data model — the important parts

### Wallet (three-bucket, double-entry)

```sql
-- source of truth: append-only journal
CREATE TABLE ledger_entries (
  id                BIGSERIAL PRIMARY KEY,
  txn_id            UUID NOT NULL,        -- groups debits + credits of one txn
  account_id        UUID NOT NULL,        -- user account or house account
  bucket            ledger_bucket NOT NULL,  -- deposit | winning | bonus | house
  amount_cents      BIGINT NOT NULL,      -- signed; sum per txn_id MUST = 0
  reason            TEXT NOT NULL,        -- entry_fee | payout | rake | refund | deposit | withdrawal | bonus_grant
  ref_type          TEXT,                 -- 'lineup' | 'contest' | 'deposit' | 'referral'
  ref_id            TEXT,
  idempotency_key   TEXT UNIQUE,          -- client-provided; retries dedupe
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
) PARTITION BY RANGE (created_at);

-- read cache; recomputed by trigger + verified nightly
CREATE TABLE balances (
  account_id  UUID NOT NULL,
  bucket      ledger_bucket NOT NULL,
  amount_cents BIGINT NOT NULL,
  updated_at  TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (account_id, bucket)
);
```

Every credit has a matching debit. `SUM(amount_cents) GROUP BY txn_id` must
equal 0. A background job asserts total issuance daily; if it ever drifts,
page.

### Lineup

```sql
CREATE TABLE lineups (
  id            UUID PRIMARY KEY,
  user_id       UUID NOT NULL,
  contest_id    UUID NOT NULL,
  entry_fee_cents BIGINT NOT NULL,
  entry_txn_id  UUID NOT NULL,          -- links to ledger
  captain_handle TEXT NOT NULL,
  vice_handle   TEXT NOT NULL,
  handles       TEXT[] NOT NULL,        -- length 6, includes captain + vice
  salary_used   INTEGER NOT NULL,
  rules_version INTEGER NOT NULL,       -- which scoring rules apply
  score         DOUBLE PRECISION,
  rank          INTEGER,
  payout_cents  BIGINT,
  payout_txn_id UUID,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  locked_at     TIMESTAMPTZ,
  UNIQUE (user_id, contest_id)
) PARTITION BY HASH (contest_id);
```

### Contest

```sql
CREATE TABLE contests (
  id              UUID PRIMARY KEY,
  cf_contest_id   INTEGER NOT NULL UNIQUE,
  title           TEXT NOT NULL,
  division        TEXT,                  -- 'Div1' | 'Div2' | 'Div3' | ...
  entry_fee_cents BIGINT NOT NULL,
  prize_structure JSONB NOT NULL,        -- payout curve versioned per contest
  rules_version   INTEGER NOT NULL,
  status          contest_status NOT NULL,
  start_at        TIMESTAMPTZ NOT NULL,
  lock_at         TIMESTAMPTZ NOT NULL,
  duration_secs   INTEGER NOT NULL,
  guaranteed_pool_cents BIGINT DEFAULT 0
);
```

---

## Event log schema

Every raw fact from CF becomes an event with a stable ordering key.

```
stream: cf.contest.<cid>.events
entry:
  ts:             unix millis
  type:           SUBMISSION_JUDGED | STANDINGS_TICK | HACK | CONTEST_PHASE
  cf_participant: handle
  problem_index:  "A" | "B" | ...
  verdict:        OK | WRONG_ANSWER | RUNTIME_ERROR | ...
  attempt_count:  int
  points:         float (CF-reported)
  penalty:        int
```

Scoring engine consumes as a Redis consumer group. Multiple scoring nodes
can pull disjoint entries concurrently for high-throughput contests.

---

## Deployment topology

### MVP (single region, 1 pod each)

- Fly.io / Railway
- Postgres: Neon serverless (auto-scales)
- Redis: Upstash serverless
- Frontend: Vercel (existing Next.js) — no change needed
- Backend: 1 pod for `api`, `ingest`, `scoring`, `gateway` each
- Cost: ~$0–$20/mo at play-money volumes

### 100k concurrent

- 2× `gateway` pods, LB
- 1× `ingest` (never more — CF limit)
- 3× `scoring` pods, consumer group
- 2× `api` pods
- Postgres: pgBouncer + 1 read replica
- Redis: single node still fine

### 1M concurrent

- 3–4× `gateway` pods (500k WS each, sticky by contest_id)
- 1× `ingest` (unchanged)
- 8× `scoring` pods
- 4× `api` pods behind LB
- Postgres: primary + 3 read replicas, partitioned tables
- Redis: Cluster, 3+ shards
- Multi-region deploy for latency; CDN in front of static leaderboard snapshots

Same code, more replicas + config. Nothing about the app has to change.

---

## What's stubbed for real-money launch

All these have an interface in Go, a `Mock*` implementation used at MVP, and
a real implementation slot ready:

- `payments.Provider` — deposit/withdrawal. `MockProvider` grants free coins.
  Real: Razorpay / Stripe.
- `kyc.Verifier` — identity check. `AlwaysPass`. Real: SumSub / Digilocker / Persona.
- `geo.Resolver` — IP → country/state. `AlwaysAllow`. Real: MaxMind / ipstack.
- `notify.Sender` — email + push. Console at MVP. Real: SES + FCM.

Every user row already has `kyc_status`, `real_money_enabled`, `country_code`,
`state_code`, `date_of_birth`. Contests already have a `real_money_enabled`
flag defaulting to `false`. The flip is a config change.

---

## What we deliberately don't do

- **Sportsbook odds.** No house-set lines. Zero bookmaker risk.
- **Live-in-play betting.** Lineups lock at contest start, no changes after.
- **Peer-to-peer wagers.** Prize pool only. No user-vs-user side bets.
- **Withdrawal to crypto.** Not at MVP. Fiat rails only.
- **Rewards for referring minors.** Age gate on every account, referred or not.

---

## Open architecture questions

- **Should scoring be idempotent per-event or per-tick?** Leaning per-tick
  (recompute lineup score from all events every N seconds) — simpler, still fast
  enough at our scale.
- **Multi-region write path?** Not needed at 1M concurrent. Postgres primary
  in one region is fine; reads are regional replicas.
- **Fraud detection.** Baseline device fingerprinting at MVP. ML-based
  collusion detection is v2.
