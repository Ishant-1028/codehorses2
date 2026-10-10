package cf

// ContestPhase mirrors the string enum returned by the Codeforces API.
type ContestPhase string

const (
	PhaseBefore            ContestPhase = "BEFORE"
	PhaseCoding            ContestPhase = "CODING"
	PhasePendingSystemTest ContestPhase = "PENDING_SYSTEM_TEST"
	PhaseSystemTest        ContestPhase = "SYSTEM_TEST"
	PhaseFinished          ContestPhase = "FINISHED"
)

// Contest is a Codeforces contest description.
type Contest struct {
	ID                  int          `json:"id"`
	Name                string       `json:"name"`
	Type                string       `json:"type"` // CF | IOI | ICPC
	Phase               ContestPhase `json:"phase"`
	Frozen              bool         `json:"frozen"`
	DurationSeconds     int64        `json:"durationSeconds"`
	StartTimeSeconds    int64        `json:"startTimeSeconds"`
	RelativeTimeSeconds int64        `json:"relativeTimeSeconds"`
}

// Problem is one task in a contest.
type Problem struct {
	ContestID int      `json:"contestId"`
	Index     string   `json:"index"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Points    float64  `json:"points"`
	Rating    int      `json:"rating"`
	Tags      []string `json:"tags"`
}

// Party is a participant (single user or team).
type Party struct {
	ContestID        int      `json:"contestId"`
	Members          []Member `json:"members"`
	ParticipantType  string   `json:"participantType"`
	Ghost            bool     `json:"ghost"`
	StartTimeSeconds int64    `json:"startTimeSeconds"`
}

// Member is one user in a Party.
type Member struct {
	Handle string `json:"handle"`
	Name   string `json:"name"`
}

// ProblemResult is a party's state on one problem in the standings.
type ProblemResult struct {
	Points                    float64 `json:"points"`
	Penalty                   int     `json:"penalty"`
	RejectedAttemptCount      int     `json:"rejectedAttemptCount"`
	Type                      string  `json:"type"` // PRELIMINARY | FINAL
	BestSubmissionTimeSeconds int64   `json:"bestSubmissionTimeSeconds"`
}

// RanklistRow is one row of the standings.
type RanklistRow struct {
	Party                     Party           `json:"party"`
	Rank                      int             `json:"rank"`
	Points                    float64         `json:"points"`
	Penalty                   int             `json:"penalty"`
	SuccessfulHackCount       int             `json:"successfulHackCount"`
	UnsuccessfulHackCount     int             `json:"unsuccessfulHackCount"`
	ProblemResults            []ProblemResult `json:"problemResults"`
	LastSubmissionTimeSeconds int64           `json:"lastSubmissionTimeSeconds"`
}

// Standings is the full standings response body.
type Standings struct {
	Contest  Contest       `json:"contest"`
	Problems []Problem     `json:"problems"`
	Rows     []RanklistRow `json:"rows"`
}

// User is a Codeforces user record.
type User struct {
	Handle       string `json:"handle"`
	Email        string `json:"email,omitempty"`
	FirstName    string `json:"firstName,omitempty"`
	LastName     string `json:"lastName,omitempty"`
	Country      string `json:"country,omitempty"`
	City         string `json:"city,omitempty"`
	Organization string `json:"organization,omitempty"`
	Rating       int    `json:"rating"`
	MaxRating    int    `json:"maxRating"`
	Rank         string `json:"rank"`
	MaxRank      string `json:"maxRank"`
	Contribution int    `json:"contribution"`
	Registered   int64  `json:"registrationTimeSeconds"`
}

// RatingChange is one entry from user.rating.
type RatingChange struct {
	ContestID               int    `json:"contestId"`
	ContestName             string `json:"contestName"`
	Handle                  string `json:"handle"`
	Rank                    int    `json:"rank"`
	RatingUpdateTimeSeconds int64  `json:"ratingUpdateTimeSeconds"`
	OldRating               int    `json:"oldRating"`
	NewRating               int    `json:"newRating"`
}

// Submission is one entry from contest.status.
type Submission struct {
	ID                  int64   `json:"id"`
	ContestID           int     `json:"contestId"`
	CreationTimeSeconds int64   `json:"creationTimeSeconds"`
	RelativeTimeSeconds int64   `json:"relativeTimeSeconds"`
	Problem             Problem `json:"problem"`
	Author              Party   `json:"author"`
	ProgrammingLanguage string  `json:"programmingLanguage"`
	Verdict             string  `json:"verdict"`
	Testset             string  `json:"testset"`
	PassedTestCount     int     `json:"passedTestCount"`
	TimeConsumedMillis  int     `json:"timeConsumedMillis"`
	MemoryConsumedBytes int64   `json:"memoryConsumedBytes"`
	Points              float64 `json:"points,omitempty"`
}
