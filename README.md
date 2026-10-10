# CodeHorses

Fantasy platform for competitive programming. Users draft a "team" of
Codeforces programmers before a round; points accrue live from real
Codeforces submissions; a prize pool splits by lineup score. Real-money
hooks are stubbed from day 1 behind interfaces.

## Status

**Scaffolded / in progress.** Architecture is written up in detail; a
functional slice of each service exists behind docker-compose. Auth flow
(email+password) is implemented. Wallet works as ledger-only (no real
payments). Not production-ready — no OAuth, no payment integration, no load
testing yet.

## What runs today

- **API service** (`services/api/`) — Go; reads/writes lineups, contests,
  wallet balances. Backed by Postgres via `PgStore` gated on
  `POSTGRES_DSN`; falls back to an in-memory store if unset.
- **Ingest service** (`services/ingest/`) — Go; single-writer over the
  Codeforces 0.5 req/sec budget, publishes submissions to Redis streams.
- **Scoring service** (`services/scoring/`) — Go; consumes events via
  Redis consumer groups (fair-share across replicas), writes point
  deltas to the double-entry ledger.
- **Gateway service** (`services/gateway/`) — Go; sticky-routed WebSocket
  fan-out per contest.
- **Ledger package** (`packages/ledger/`) — append-only journal, partitioned
  by contest; balances are derived, not stored. Postgres + in-memory
  backends both implement the same interface.
- **CF client** (`packages/cf/`) — token-bucket rate limiter over the
  Codeforces public API, signed requests, typed responses.
- **Scoring rules** (`packages/scoring-rules/`) — pure function from
  event log to lineup score. Any scoring bug is fixable by replaying.
- **Frontend** (`app/`) — Next.js 15 App Router. Dashboard, wallet,
  login, contest pages. Email+password auth flow (signup, login, logout).
- **Load balancer** (`docker/`) — nginx with sticky WebSocket routing by
  contest_id. Deployed in docker-compose; scales horizontally with gateway.

## Not yet built

- OAuth providers (Google, GitHub). Current auth is email+password only.
- Real-money payment integration (wallet is a ledger balance today; no
  KYC, no PSP hook wired). Admin can credit wallets via `/v1/deposit/mock`.
- Load / soak testing at the 1M concurrent scale claimed in the arch doc.

## Run

```bash
docker compose up --build

# Scale out stateless services:
docker compose up --scale scoring=4 --scale gateway=3 --scale api=2
# DO NOT scale `ingest` — it owns the CF rate-limit budget.
```

Frontend dev:

```bash
npm install
npm run dev
```

## Docs

- [ARCHITECTURE.md](ARCHITECTURE.md) — target architecture, scale story,
  component map, event flow.
- [AGENTS.md](AGENTS.md) — this Next.js is not the Next.js you know;
  read docs bundled under `node_modules/next/dist/docs/` before touching
  the frontend.
- [docs/adr/](docs/adr/) — architecture decision records.

## Design principles

1. **Event log is the source of truth.** Codeforces submissions become
   internal events; scoring is a pure function of the event log.
2. **Single writer per external limit.** One ingest worker owns the CF
   API budget for the whole product — never fan out CF calls per user.
3. **Broadcast, not fan-out.** Leaderboards are per-contest; cost of
   pushing an update is O(contests active), not O(users watching).
4. **Money moves through a double-entry ledger.** Balances are derived
   from an append-only journal.
5. **Real-money hooks stubbed from day 1.** Wallet, KYC, geo, payments
   live behind interfaces with mock implementations.

## Tech

Go 1.22+ (backend services and packages) · Next.js 15 App Router
(frontend) · Postgres 16 (ledger, users, contests) · Redis 7 (event
stream between ingest and scoring) · docker-compose (local dev, demo
horizontal scaling).
