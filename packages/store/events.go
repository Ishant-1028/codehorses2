// Package store defines the persistence contracts used across services.
//
// The event log is a Redis Stream, keyed per-contest. Ingest writes to
// it, scoring consumes from a consumer group. Concrete Redis wiring
// lives in each service; this package holds only the shared types and
// stream key helpers so that every producer/consumer agrees on the shape.
package store

import "fmt"

// EventType distinguishes categories of raw CF facts.
type EventType string

const (
	EventStandingsTick EventType = "standings_tick"
	EventPhaseChange   EventType = "phase_change"
	EventFinalResults  EventType = "final_results"
)

// Event is one record on the log. Fields are strings so it round-trips
// cleanly through XADD without a schema registry.
type Event struct {
	Type      EventType `redis:"type"`
	ContestID int       `redis:"contest_id"`
	Timestamp int64     `redis:"ts"`
	// PayloadJSON is a snapshot of the CF response fragment (e.g. the
	// full standings body, or a phase-transition tuple).
	PayloadJSON string `redis:"payload"`
}

// EventStreamKey returns the Redis Stream key for one CF contest.
// Sharding: Redis Cluster hashes on the key, so all events for one
// contest land on one node. That is the desired locality — the scoring
// engine reads sequentially from one stream per contest.
func EventStreamKey(cfContestID int) string {
	return fmt.Sprintf("cf.contest.%d.events", cfContestID)
}

// LeaderboardKey returns the Redis ZSET key for the live leaderboard.
func LeaderboardKey(internalContestID string) string {
	return fmt.Sprintf("leaderboard:%s", internalContestID)
}

// LeaderboardChannel returns the pub/sub channel gateway subscribes to.
func LeaderboardChannel(internalContestID string) string {
	return fmt.Sprintf("contest.%s.leaderboard", internalContestID)
}

// ConsumerGroup is the scoring engine's stream consumer group name.
// One group per contest so we can restart scoring without losing offset.
func ConsumerGroup(cfContestID int) string {
	return fmt.Sprintf("scoring.contest.%d", cfContestID)
}
