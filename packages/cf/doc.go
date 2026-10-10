// Package cf is a rate-limited Codeforces API client.
//
// The Codeforces public API is capped at one request per two seconds
// (0.5 req/sec) globally per IP. This package owns that budget for the
// entire product: every caller shares one limiter, requests wait in a
// FIFO queue, and retries respect the same governor.
//
// It is authenticated (SHA-512 signed) so that endpoints like
// contest.standings with the `handles` parameter (up to 10,000 handles
// per call) are usable. That batch parameter is what makes the product
// viable at scale: one poll can score all users.
//
// The package is transport-only. It does not persist anything; the
// ingest worker is responsible for turning API responses into events on
// the internal log.
package cf
