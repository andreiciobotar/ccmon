// Package data runs the Python ccmon CLI and decodes its JSON output.
package data

import "time"

// Usage is one set of token counts.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheWrite int64 `json:"cache_write"`
	CacheRead  int64 `json:"cache_read"`
	Total      int64 `json:"total"`
}

// Add accumulates o into u.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheWrite += o.CacheWrite
	u.CacheRead += o.CacheRead
	u.Total += o.Total
}

// Tokens is the main/subagent split of a session.
type Tokens struct {
	Total       int64   `json:"total"`
	Main        int64   `json:"main"`
	Subagents   int64   `json:"subagents"`
	MainPct     float64 `json:"main_pct"`
	SubagentPct float64 `json:"subagent_pct"`
}

// Recheck is the explorer re-check signal.
type Recheck struct {
	ExplorersWithCitations int      `json:"explorer_with_citations"`
	Rechecked              int      `json:"rechecked"`
	Rate                   *float64 `json:"rate"`
}

// ReviewLoopEvent is one reviewer verdict answered by a coder dispatch or resume.
type ReviewLoopEvent struct {
	VerdictAt         *time.Time `json:"verdict_at"`
	ReviewerAgentID   string     `json:"reviewer_agent_id"`
	ReviewerToolUseID string     `json:"reviewer_tool_use_id"`
	CoderAgentID      string     `json:"coder_agent_id"`
	CoderToolUseID    string     `json:"coder_tool_use_id"`
	Via               string     `json:"via"`
	At                *time.Time `json:"at"`
}

// Dispatch is one subagent run.
type Dispatch struct {
	ToolUseID           string           `json:"tool_use_id"`
	AgentID             string           `json:"agent_id"`
	AgentType           string           `json:"agent_type"`
	Description         string           `json:"description"`
	PromptChars         int              `json:"prompt_chars"`
	RequestedModel      *string          `json:"requested_model"`
	Depth               int              `json:"depth"`
	ParentAgentID       *string          `json:"parent_agent_id"`
	Status              string           `json:"status"`
	Start               *time.Time       `json:"start"`
	ReturnedAt          *time.Time       `json:"returned_at"`
	DurationMS          *float64         `json:"duration_ms"`
	Models              []string         `json:"models"`
	Usage               Usage            `json:"usage"`
	UsageByModel        map[string]Usage `json:"usage_by_model"`
	Resumes             int              `json:"resumes"`
	Flags               []string         `json:"flags"`
	ReviewLoopsDispatch int              `json:"review_loops_dispatch"`
	ReviewLoopsResume   int              `json:"review_loops_resume"`
	RepeatOf            *string          `json:"repeat_of"`
	RepeatScore         *float64         `json:"repeat_score"`
	Warnings            []string         `json:"warnings"`
}

// Session is the output of `ccmon sessions` (one element) or `ccmon session`.
// Dispatches is only present for the detail form.
type Session struct {
	ID              string            `json:"id"`
	Project         string            `json:"project"`
	ProjectDir      string            `json:"project_dir"`
	Cwd             string            `json:"cwd"`
	Path            string            `json:"path"`
	Start           *time.Time        `json:"start"`
	End             *time.Time        `json:"end"`
	Tokens          Tokens            `json:"tokens"`
	ByModel         map[string]Usage  `json:"by_model"`
	ByAgent         map[string]Usage  `json:"by_agent"`
	DispatchCount   int               `json:"dispatch_count"`
	Repeats         int               `json:"repeats"`
	ReviewLoops     int               `json:"review_loops"`
	ReviewLoopEvent []ReviewLoopEvent `json:"review_loop_events"`
	Recheck         Recheck           `json:"recheck"`
	WarnCount       int               `json:"warn_count"`
	Warnings        []string          `json:"warnings"`
	Malformed       int               `json:"malformed"`
	Dispatches      []Dispatch        `json:"dispatches"`

	// Local, not from JSON: the transcripts' newest mtime observed before this was read.
	// A result with an older value never replaces one with a newer value.
	LastChange time.Time `json:"-"`
}

// SessionsResult is `ccmon sessions --json`.
type SessionsResult struct {
	Sessions  []Session `json:"sessions"`
	Malformed int       `json:"malformed"`
}

// AgentRow is one element of `ccmon agents --json`.
type AgentRow struct {
	Agent        string           `json:"agent"`
	Dispatches   int              `json:"dispatches"`
	Models       []string         `json:"models"`
	Usage        Usage            `json:"usage"`
	UsageByModel map[string]Usage `json:"usage_by_model"`
	TokensP50    *float64         `json:"tokens_p50"`
	TokensP90    *float64         `json:"tokens_p90"`
	DurationP50  *float64         `json:"duration_ms_p50"`
	DurationP90  *float64         `json:"duration_ms_p90"`
	Repeats      int              `json:"repeats"`
	ReviewLoops  int              `json:"review_loops"`
	WarnCount    int              `json:"warn_count"`
}

// Agents is `ccmon agents --json`.
type Agents struct {
	Agents    []AgentRow       `json:"agents"`
	ByModel   map[string]Usage `json:"by_model"`
	Sessions  int              `json:"sessions"`
	Malformed int              `json:"malformed"`
}

// Duration returns the dispatch duration, estimating running ones from now.
func (d Dispatch) Duration(now time.Time) (time.Duration, bool) {
	if d.DurationMS != nil {
		return time.Duration(*d.DurationMS * float64(time.Millisecond)), true
	}
	if d.Status == "running" && d.Start != nil {
		return now.Sub(*d.Start), true
	}
	return 0, false
}
