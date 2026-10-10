// Package scoringrules turns Codeforces standings into fantasy points.
//
// Rules are versioned. Each contest records which version was used to
// score it, so payouts remain auditable when the ruleset changes.
//
// Version 1 (current):
//   - +20 × problem_index for each solved problem (A=1, B=2, ...)
//   - −5 × problem_index per wrong submission
//   - +50 per successful hack, −25 per unsuccessful hack
//   - +100 bonus for a finish in the top 100
//   - +0.5 per Elo point gained (rating losses do not subtract)
//   - Captain earns 2× and vice-captain 1.5× on all of the above
//
// Salary formula (deterministic, public):
//
//	salary = clamp(round((rating - 1400) / 100), 3, 40)
package scoringrules

import "math"

// Version identifies a ruleset. Bump it whenever the semantics change.
type Version int

const (
	V1 Version = 1
	// Latest is the ruleset used for new contests.
	Latest = V1
)

// PlayerPerformance is what the scoring engine feeds in per drafted
// programmer. All fields are cumulative in the current standings.
type PlayerPerformance struct {
	Handle            string
	Solved            map[string]bool // problem index → solved
	WrongAttempts     map[string]int  // problem index → wrong submissions before AC
	SuccessfulHacks   int
	UnsuccessfulHacks int
	FinalRank         int // 0 if contest not yet finished
	RatingDelta       int // 0 while contest is CODING
}

// LineupPerformance is one user's whole team.
type LineupPerformance struct {
	Players         []PlayerPerformance
	CaptainHandle   string
	ViceCaptainHand string
}

// PlayerBreakdown is a per-player itemised score, useful for the UI.
type PlayerBreakdown struct {
	Handle     string
	Base       float64
	Multiplier float64
	Total      float64
	Detail     Detail
}

// Detail lists the components that produced Base.
type Detail struct {
	SolvePoints  float64
	WrongPenalty float64
	HackPoints   float64
	TopBonus     float64
	RatingBonus  float64
}

// Result is the full scoring output for a lineup.
type Result struct {
	Version Version
	Total   float64
	Players []PlayerBreakdown
}

// Score computes a full result under the requested version. Unknown
// versions fall back to Latest (safe because scoring is a pure function
// and callers store the version alongside the result).
func Score(v Version, lineup LineupPerformance) Result {
	if v == 0 {
		v = Latest
	}
	switch v {
	case V1:
		return scoreV1(lineup)
	default:
		return scoreV1(lineup)
	}
}

func scoreV1(lineup LineupPerformance) Result {
	out := Result{Version: V1, Players: make([]PlayerBreakdown, 0, len(lineup.Players))}
	for _, p := range lineup.Players {
		d := detailV1(p)
		base := d.SolvePoints + d.WrongPenalty + d.HackPoints + d.TopBonus + d.RatingBonus
		mult := 1.0
		switch p.Handle {
		case lineup.CaptainHandle:
			mult = 2.0
		case lineup.ViceCaptainHand:
			mult = 1.5
		}
		total := base * mult
		out.Players = append(out.Players, PlayerBreakdown{
			Handle:     p.Handle,
			Base:       base,
			Multiplier: mult,
			Total:      total,
			Detail:     d,
		})
		out.Total += total
	}
	return out
}

func detailV1(p PlayerPerformance) Detail {
	var d Detail
	for idx, solved := range p.Solved {
		if solved {
			d.SolvePoints += 20 * float64(problemWeight(idx))
		}
	}
	for idx, n := range p.WrongAttempts {
		if n > 0 {
			d.WrongPenalty -= 5 * float64(n) * float64(problemWeight(idx))
		}
	}
	d.HackPoints = 50*float64(p.SuccessfulHacks) - 25*float64(p.UnsuccessfulHacks)
	if p.FinalRank > 0 && p.FinalRank <= 100 {
		d.TopBonus = 100
	}
	if p.RatingDelta > 0 {
		d.RatingBonus = 0.5 * float64(p.RatingDelta)
	}
	return d
}

// problemWeight maps a Codeforces index ("A", "B", "C1", …) to its
// difficulty rank (1-based). Unknown / non-alphabetic indices fall back
// to 1 so a bad standings payload never divides by zero.
func problemWeight(idx string) int {
	if idx == "" {
		return 1
	}
	c := idx[0]
	if c < 'A' || c > 'Z' {
		return 1
	}
	return int(c-'A') + 1
}

// Salary is the deterministic team-builder cost of one programmer.
func Salary(rating int) int {
	raw := math.Round(float64(rating-1400) / 100.0)
	if raw < 3 {
		return 3
	}
	if raw > 40 {
		return 40
	}
	return int(raw)
}

// TeamSize and SalaryCap are enforced at lineup creation. Kept here so
// the frontend can import the same numbers via a generated JSON snapshot.
const (
	TeamSize      = 6
	SalaryCap     = 100
	MaxPerCountry = 2
)
