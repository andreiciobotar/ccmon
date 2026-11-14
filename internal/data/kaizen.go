package data

import "context"

// Kaizen is the document `isshoni kaizen --json` prints: the measures of whether the
// factory is getting better. A JSON null decodes to the zero value, or to nil where a
// number or a record may be absent.
type Kaizen struct {
	Generated   string        `json:"generated"`
	FactoryDir  string        `json:"factory_dir"`
	StoreFound  bool          `json:"store_found"`
	Prices      Prices        `json:"prices"`
	Repos       []KaizenRepo  `json:"repos"`
	Versions    []KaizenVer   `json:"versions"`
	Tasks       []KaizenTask  `json:"tasks"`
	Summary     KaizenSummary `json:"summary"`
	Stops       []KaizenStop  `json:"stops"`
	Slowed      []KaizenSlow  `json:"slowed"`
	Rewards     []KaizenAward `json:"rewards"`
	Outbox      KaizenOutbox  `json:"outbox"`
	SiteManager SiteManager   `json:"site_manager"`
}

// SiteManager is the site manager's record as `factory firings` counts it, over every
// repository: precision is acted/(acted+ignored), recall acted/(acted+missed), nil when
// there is nothing to divide by.
type SiteManager struct {
	Escalated        int      `json:"escalated"`
	Acted            int      `json:"acted"`
	Ignored          int      `json:"ignored"`
	NoOutcome        int      `json:"no_outcome"`
	Suppressed       int      `json:"suppressed"`
	Missed           int      `json:"missed"`
	MissedSuppressed int      `json:"missed_suppressed"`
	MissedNeverFired int      `json:"missed_never_fired"`
	Precision        *float64 `json:"precision"`
	Recall           *float64 `json:"recall"`
}

// KaizenRepo says whether the pull requests of one repository could be read.
type KaizenRepo struct {
	Repo     string `json:"repo"`
	RepoPath string `json:"repo_path"`
	PRs      string `json:"prs"` // "ok", else why gh could not be used
}

type KaizenVer struct {
	Version   string `json:"version"`
	FirstSeen string `json:"first_seen"`
}

// Roles is money by role.
type Roles struct {
	Lead     float64 `json:"lead"`
	Coder    float64 `json:"coder"`
	Reviewer float64 `json:"reviewer"`
	Explorer float64 `json:"explorer"`
	Other    float64 `json:"other"`
}

// Defects counts retro defects by who caught them.
type Defects struct {
	Tests    int `json:"tests"`
	Reviewer int `json:"reviewer"`
	Lead     int `json:"lead"`
	CI       int `json:"ci"`
	Human    int `json:"human"`
	Other    int `json:"other"`
}

type KaizenCost struct {
	Total          float64 `json:"total"`
	UnpricedTokens int64   `json:"unpriced_tokens"`
	ByRole         Roles   `json:"by_role"`
	Rework         float64 `json:"rework"`
}

type KaizenPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Opened string `json:"opened"`
	Merged string `json:"merged"`
}

type KaizenTask struct {
	Repo      string      `json:"repo"`
	RepoPath  string      `json:"repo_path"`
	Slug      string      `json:"slug"`
	Version   string      `json:"version"`
	Started   string      `json:"started"`
	State     string      `json:"state"`
	PR        *KaizenPR   `json:"pr"`
	Landed    bool        `json:"landed"`
	BriefToPR *float64    `json:"brief_to_pr_s"`
	PRToMerge *float64    `json:"pr_to_merge_s"`
	Cost      *KaizenCost `json:"cost"`
	Rounds    *int        `json:"review_rounds"`
	FirstPass *bool       `json:"first_pass"`
	Stops     int         `json:"stops"`
	StoppedS  float64     `json:"stopped_s"`
	Defects   *Defects    `json:"defects"`
}

// Median is a median over the n landed tasks that have the value; nil when n is 0.
type Median struct {
	Median *float64 `json:"median"`
	N      int      `json:"n"`
}

// Pair is a measure's median over the latest landed tasks and over the earlier ones.
type Pair struct {
	Latest  *float64 `json:"latest"`
	Earlier *float64 `json:"earlier"`
}

// KaizenTrend compares the latest half of the landed tasks with the earlier half.
type KaizenTrend struct {
	LatestN   int  `json:"latest_n"`
	EarlierN  int  `json:"earlier_n"`
	Cost      Pair `json:"cost"`
	BriefToPR Pair `json:"brief_to_pr_s"`
	PRToMerge Pair `json:"pr_to_merge_s"`
	Rounds    Pair `json:"review_rounds"`
}

type KaizenTime struct {
	Building  float64 `json:"building_s"`
	Stopped   float64 `json:"stopped_s"`
	MergeWait float64 `json:"merge_wait_s"`
}

type KaizenSummary struct {
	Tasks     int    `json:"tasks"`
	Landed    int    `json:"landed"`
	InFlight  int    `json:"in_flight"`
	Cost      Median `json:"cost"`
	BriefToPR Median `json:"brief_to_pr_s"`
	PRToMerge Median `json:"pr_to_merge_s"`
	Rounds    Median `json:"review_rounds"`
	FirstPass struct {
		Yes int `json:"yes"`
		Of  int `json:"of"`
	} `json:"first_pass"`
	ReworkShare *float64     `json:"rework_share"`
	ByRole      Roles        `json:"by_role"`
	Time        KaizenTime   `json:"time"`
	Defects     Defects      `json:"defects"`
	TrendMin    int          `json:"trend_min"`
	Trend       *KaizenTrend `json:"trend"`
}

// KaizenStop is a moment the line stopped for the human, or a permission was denied.
type KaizenStop struct {
	TS      string   `json:"ts"`
	Repo    string   `json:"repo"`
	Slug    string   `json:"slug"`
	Kind    string   `json:"kind"` // QUESTION, BLOCKED, OVERTIME, DENIED
	WaitedS *float64 `json:"waited_s"`
	Why     string   `json:"why"`
}

type KaizenSlow struct {
	TS   string `json:"ts"`
	Repo string `json:"repo"`
	Slug string `json:"slug"`
	Text string `json:"text"`
}

type KaizenAward struct {
	Kind string `json:"kind"` // first_pass_streak, record_low_cost, record_fast
	Slug string `json:"slug"`
	Text string `json:"text"`
}

// OutboxCounts is entries by status.
type OutboxCounts struct {
	Open     int `json:"open"`
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Done     int `json:"done"`
}

type OutboxTopic struct {
	Topic string `json:"topic"`
	Open  int    `json:"open"`
}

type OutboxEntry struct {
	ID     string `json:"id"`
	TS     string `json:"ts"`
	Repo   string `json:"repo"`
	Slug   string `json:"slug"`
	Role   string `json:"role"`
	Kind   string `json:"kind"` // issue, decision
	Status string `json:"status"`
	Topic  string `json:"topic"` // issues only
	Text   string `json:"text"`
	PR     string `json:"pr"`
}

type KaizenOutbox struct {
	Issues    OutboxCounts  `json:"issues"`
	Decisions OutboxCounts  `json:"decisions"`
	Topics    []OutboxTopic `json:"topics"`
	Entries   []OutboxEntry `json:"entries"`
}

// Kaizen runs `isshoni kaizen [--since X] --json`.
func (r *Runner) Kaizen(ctx context.Context, since string) (*Kaizen, error) {
	var out Kaizen
	args := append([]string{"kaizen"}, sinceArgs(since)...)
	if err := r.run(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}
