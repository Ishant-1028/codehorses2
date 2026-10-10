package scoringrules

import (
	"math"
	"testing"
)

func TestSalary_ClampsAndScales(t *testing.T) {
	cases := []struct {
		rating int
		want   int
	}{
		{800, 3},   // clamp low
		{1600, 3},  // (1600-1400)/100 = 2 → clamp to 3
		{1700, 3},  // rounds to 3
		{2400, 10}, // (2400-1400)/100 = 10
		{3800, 24},
		{9999, 40}, // clamp high
	}
	for _, tc := range cases {
		if got := Salary(tc.rating); got != tc.want {
			t.Errorf("Salary(%d)=%d, want %d", tc.rating, got, tc.want)
		}
	}
}

func TestScoreV1_SolveAndCaptainMultiplier(t *testing.T) {
	lineup := LineupPerformance{
		CaptainHandle:   "tourist",
		ViceCaptainHand: "jiangly",
		Players: []PlayerPerformance{
			{
				Handle: "tourist",
				Solved: map[string]bool{"A": true, "B": true, "F": true},
			},
			{
				Handle:        "jiangly",
				Solved:        map[string]bool{"A": true, "C": true},
				WrongAttempts: map[string]int{"D": 3},
			},
			{
				Handle:          "ecnerwala",
				Solved:          map[string]bool{"A": true},
				SuccessfulHacks: 2,
			},
		},
	}
	got := Score(V1, lineup)

	// tourist: A=20 + B=40 + F=120 = 180, x2 captain = 360
	// jiangly: A=20 + C=60 = 80; wrong D×3 = -60; total 20; x1.5 = 30
	// ecnerwala: A=20 + hacks 100 = 120; x1 = 120
	if !approx(got.Total, 510) {
		t.Fatalf("total=%.2f want 510", got.Total)
	}
	if got.Players[0].Multiplier != 2.0 {
		t.Fatalf("captain multiplier wrong")
	}
}

func TestScoreV1_TopBonusAndRatingBonus(t *testing.T) {
	lineup := LineupPerformance{
		Players: []PlayerPerformance{
			{
				Handle:      "x",
				FinalRank:   42,
				RatingDelta: 120,
			},
		},
	}
	got := Score(V1, lineup)
	// top-100 bonus 100 + rating 60 = 160
	if !approx(got.Total, 160) {
		t.Fatalf("total=%.2f want 160", got.Total)
	}
}

func TestScoreV1_RatingLossIsFree(t *testing.T) {
	lineup := LineupPerformance{
		Players: []PlayerPerformance{
			{Handle: "x", RatingDelta: -75},
		},
	}
	got := Score(V1, lineup)
	if !approx(got.Total, 0) {
		t.Fatalf("total=%.2f want 0", got.Total)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
