package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"isshoni/internal/data"
)

type viewID int

const (
	viewDashboard viewID = iota
	viewSession
	viewAgents
)

type sortMode int

const (
	sortRecent sortMode = iota
	sortTokens
	sortShare
)

func (s sortMode) String() string {
	return [...]string{"recent", "tokens", "subagent share"}[s]
}

type rangeOpt struct{ label, since string }

var ranges = []rangeOpt{{"24h", "24h"}, {"7d", "7d"}, {"30d", "30d"}, {"all", ""}}

const (
	liveWindow  = 30 * time.Second
	agentsEvery = 20 * time.Second
	flashFor    = 1500 * time.Millisecond
	toastFor    = 4 * time.Second
	tickFast    = 80 * time.Millisecond
	tickSlow    = 400 * time.Millisecond
	tickIdle    = time.Second // redraw interval when nothing moves
	pollEvery   = time.Second // transcript mtime poll interval
	minWidth    = 60
	minHeight   = 16
)

// Options configures a model.
type Options struct {
	Runner  *data.Runner
	Ctx     context.Context // cancelled on quit; every python call runs under it
	Range   string          // initial range label, default 7d
	NoColor bool
	Follow  bool
	Static  bool // render settled (snapshots, tests): no animation phase, no clock
	Now     func() time.Time
}

type toast struct {
	text  string
	err   bool
	until time.Time
}

type model struct {
	opts   Options
	th     *Theme
	runner *data.Runner
	now    func() time.Time
	w, h   int

	ctx        context.Context // lives until quit
	loadCtx    context.Context // lives until the next load replaces it
	loadCancel context.CancelFunc

	view     viewID
	help     bool
	rangeIdx int
	sortBy   sortMode
	follow   bool
	filter   string
	filterIn bool // typing in the filter box

	sessions map[string]*data.Session // list entries: summaries, no dispatches
	details  map[string]*data.Session // per-session results with dispatches
	agents   *data.Agents

	selID      string
	top        int // first visible session row
	dcursor    int // dispatch cursor in session view
	dtop       int
	detailOnly bool // narrow terminals: enter swaps the list for the detail pane
	acursor    int
	atop       int

	loading     bool
	loadGen     int
	fatal       error // python or package missing: nothing can load
	refreshErr  string
	lastUpdate  time.Time
	agentsBusy  bool
	agentsDirty bool
	agentsAt    time.Time // when the agents aggregate last finished, ok or not

	// seen is the newest transcript mtime per session id as of the last scan; nil until
	// the first load has finished, which is also when polling starts.
	seen     map[string]time.Time
	loaded   bool   // the first load has finished, whatever its outcome
	scanErr  string // the last scan failed (the projects root cannot be read): data is stale
	scanning bool
	polledAt time.Time
	// retry: per-session refreshes that failed, with when; tried again every agentsEvery.
	retry map[string]time.Time
	// dirty: changed while its refresh was in flight. inflight: a detail fetch is running.
	dirty    map[string]bool
	inflight map[string]bool

	counter map[string]*Counter
	rowTok  map[string]*Counter
	flash   map[string]time.Time
	toast   toast

	tickOn   bool
	tickGen  int           // a tick carrying an older generation was superseded
	tickD    time.Duration // delay of the pending tick
	tickFrom time.Time     // when the pending tick was scheduled
}

// New builds the model.
func New(o Options) *model {
	if o.Range == "" {
		o.Range = "7d"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Ctx == nil {
		o.Ctx = context.Background()
	}
	idx := 1
	for i, r := range ranges {
		if r.label == o.Range {
			idx = i
		}
	}
	m := &model{
		opts: o, th: NewTheme(o.NoColor), runner: o.Runner, now: o.Now, ctx: o.Ctx,
		w: 100, h: 30, rangeIdx: idx, follow: o.Follow, loading: true,
		sessions: map[string]*data.Session{}, details: map[string]*data.Session{},
		dirty: map[string]bool{}, inflight: map[string]bool{}, retry: map[string]time.Time{}, counter: map[string]*Counter{},
		rowTok: map[string]*Counter{}, flash: map[string]time.Time{},
	}
	m.loadCtx, m.loadCancel = context.WithCancel(m.ctx)
	for _, k := range []string{"tokens", "dispatches", "repeats", "loops", "warns", "share", "recheck"} {
		m.counter[k] = &Counter{}
	}
	if o.Runner != nil {
		m.fatal = o.Runner.Check()
	}
	return m
}

// ---- messages ----------------------------------------------------------------

type sessionsMsg struct {
	gen     int
	res     *data.SessionsResult
	seen    map[string]time.Time // mtimes taken before the read
	err     error
	scanErr error // the scan behind seen failed
}
type agentsMsg struct {
	gen int
	res *data.Agents
	err error
}
type detailMsg struct {
	id  string
	at  time.Time // mtime known when the read started
	res *data.Session
	err error
}
type scanMsg struct {
	seen map[string]time.Time
	err  error
}
type tickMsg struct{ gen int }

// ---- commands ----------------------------------------------------------------

// reload starts a full load for the current range, cancelling the one it replaces.
func (m *model) reload() tea.Cmd {
	if m.runner == nil || m.fatal != nil {
		return nil
	}
	m.loadCancel()
	m.loadCtx, m.loadCancel = context.WithCancel(m.ctx)
	m.loadGen++
	m.loading = true
	if len(m.sessions) == 0 {
		m.refreshErr = ""
	}
	m.agentsBusy = false
	return tea.Batch(m.loadSessionsCmd(), m.loadAgentsCmd())
}

func (m *model) loadSessionsCmd() tea.Cmd {
	r, ctx, since, gen := m.runner, m.loadCtx, ranges[m.rangeIdx].since, m.loadGen
	return func() tea.Msg {
		// stat first: anything written after this shows up as a change in the poll
		seen, scanErr := data.Scan(r.Projects())
		res, err := r.Sessions(ctx, since)
		if err == nil {
			for i := range res.Sessions {
				res.Sessions[i].LastChange = seen[res.Sessions[i].ID]
			}
		}
		return sessionsMsg{gen: gen, res: res, seen: seen, err: err, scanErr: scanErr}
	}
}

func (m *model) loadAgentsCmd() tea.Cmd {
	m.agentsBusy = true
	r, ctx, since, gen := m.runner, m.loadCtx, ranges[m.rangeIdx].since, m.loadGen
	return func() tea.Msg {
		res, err := r.Agents(ctx, since)
		return agentsMsg{gen: gen, res: res, err: err}
	}
}

// fetchSession is the only way a detail fetch starts. A fetch already running for the
// session is not doubled: the session is marked dirty and fetched once more when the
// running one returns.
func (m *model) fetchSession(id string) tea.Cmd {
	if m.runner == nil || m.fatal != nil {
		return nil
	}
	if m.inflight[id] {
		m.dirty[id] = true
		return nil
	}
	m.inflight[id] = true
	delete(m.dirty, id)
	delete(m.retry, id)
	r, ctx, at := m.runner, m.ctx, m.seen[id]
	return func() tea.Msg {
		res, err := r.Session(ctx, id)
		return detailMsg{id: id, at: at, res: res, err: err}
	}
}

func (m *model) scanCmd() tea.Cmd {
	m.scanning = true
	root := m.runner.Projects()
	return func() tea.Msg {
		seen, err := data.Scan(root)
		return scanMsg{seen: seen, err: err}
	}
}

// ensureTick keeps one clock running at the pace the screen needs. A pending tick that
// is slower than needed (an animation just started during idle) is superseded.
func (m *model) ensureTick() tea.Cmd {
	if m.opts.Static {
		return nil
	}
	d := tickIdle
	switch {
	case m.animating():
		d = tickFast
	case m.pulsing():
		d = tickSlow
	}
	if m.tickOn && m.tickD <= d {
		return nil
	}
	m.tickOn, m.tickD, m.tickFrom = true, d, m.now()
	m.tickGen++
	gen := m.tickGen
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{gen} })
}

// Init starts the first load and the clock.
func (m *model) Init() tea.Cmd {
	return tea.Batch(m.reload(), m.ensureTick())
}

// ---- derived state -------------------------------------------------------------

// activity is when the session last moved: its newest transcript write or its end.
func (m *model) activity(s *data.Session) time.Time {
	t := m.seen[s.ID]
	if s.End != nil && s.End.After(t) {
		t = *s.End
	}
	return t
}

func (m *model) isLive(s *data.Session) bool {
	return m.now().Sub(m.activity(s)) < liveWindow
}

func (m *model) liveCount() int {
	n := 0
	for _, s := range m.sessions {
		if m.isLive(s) {
			n++
		}
	}
	return n
}

type rowKind int

const (
	kindRepo rowKind = iota
	kindTask
	kindSession
)

// treeRow is one display row of the sessions table: a repository, a task or a session.
// A group of exactly one session is merged into that session's row: s is set, the kind stays the group's.
type treeRow struct {
	kind   rowKind
	s      *data.Session   // set on session rows and merged rows only: only these can be selected
	name   string          // repository, task or 8-character session id
	suffix string          // faint, after the name: ×N sessions of a group, the id of a merged row
	depth  int             // indent in steps of two cells
	group  []*data.Session // what the row totals; a session row holds just itself
}

// rankKey is what a sort mode compares: a session or the sum of a group.
type rankKey struct {
	tokens, sub int64
	act         time.Time
}

type unit struct {
	group     []*data.Session
	name, tie string
	key       rankKey
}

func (m *model) unit(group []*data.Session, name, tie string) unit {
	u := unit{group: group, name: name, tie: tie}
	for _, s := range group {
		u.key.tokens += s.Tokens.Total
		u.key.sub += s.Tokens.Subagents
		if a := m.activity(s); a.After(u.key.act) {
			u.key.act = a
		}
	}
	return u
}

// sortUnits orders units by the sort mode, then latest activity, then name.
func (m *model) sortUnits(us []unit) {
	share := func(k rankKey) float64 {
		if k.tokens == 0 {
			return 0
		}
		return float64(k.sub) / float64(k.tokens)
	}
	sort.Slice(us, func(i, j int) bool {
		a, b := us[i], us[j]
		switch m.sortBy {
		case sortTokens:
			if a.key.tokens != b.key.tokens {
				return a.key.tokens > b.key.tokens
			}
		case sortShare:
			if sa, sb := share(a.key), share(b.key); sa != sb {
				return sa > sb
			}
		}
		if !a.key.act.Equal(b.key.act) {
			return a.key.act.After(b.key.act)
		}
		if a.name != b.name {
			return a.name < b.name
		}
		return a.tie < b.tie
	})
}

// sessionUnits makes one unit per session, named by its id.
func (m *model) sessionUnits(ss []*data.Session) []unit {
	us := make([]unit, len(ss))
	for i, s := range ss {
		us[i] = m.unit([]*data.Session{s}, s.ID, "")
	}
	m.sortUnits(us)
	return us
}

// rows is the filtered, sorted sessions table, the one owner of display order:
// repositories, each with its sessions without a task, then its tasks.
func (m *model) rows() []treeRow {
	f := strings.ToLower(strings.TrimSpace(m.filter))
	type repo struct {
		name, path string
		bare       []*data.Session
		tasks      map[string][]*data.Session
		all        []*data.Session
	}
	byRepo := map[[2]string]*repo{}
	for _, s := range m.sessions {
		if f != "" && !strings.Contains(strings.ToLower(s.Label()+" "+s.Project+" "+s.ID), f) {
			continue
		}
		k := [2]string{s.RepoPath, s.Repo}
		r := byRepo[k]
		if r == nil {
			r = &repo{name: s.Repo, path: s.RepoPath, tasks: map[string][]*data.Session{}}
			byRepo[k] = r
		}
		r.all = append(r.all, s)
		if s.Task == "" {
			r.bare = append(r.bare, s)
		} else {
			r.tasks[s.Task] = append(r.tasks[s.Task], s)
		}
	}
	var repos []unit
	for _, r := range byRepo {
		repos = append(repos, m.unit(r.all, r.name, r.path))
	}
	m.sortUnits(repos)
	var out []treeRow
	for _, ru := range repos {
		r := byRepo[[2]string{ru.tie, ru.name}]
		if len(r.all) == 1 && len(r.bare) == 1 {
			out = append(out, treeRow{kind: kindRepo, s: r.all[0], name: r.name, suffix: short8(r.all[0].ID), group: ru.group})
			continue
		}
		out = append(out, treeRow{kind: kindRepo, name: r.name, suffix: fmt.Sprintf("×%d", len(ru.group)), group: ru.group})
		for _, u := range m.sessionUnits(r.bare) {
			out = append(out, treeRow{kind: kindSession, s: u.group[0], name: short8(u.name), depth: 1, group: u.group})
		}
		var tasks []unit
		for name, ss := range r.tasks {
			tasks = append(tasks, m.unit(ss, name, ""))
		}
		m.sortUnits(tasks)
		for _, tu := range tasks {
			if len(tu.group) == 1 {
				out = append(out, treeRow{kind: kindTask, s: tu.group[0], name: tu.name, suffix: short8(tu.group[0].ID), depth: 1, group: tu.group})
				continue
			}
			out = append(out, treeRow{kind: kindTask, name: tu.name, suffix: fmt.Sprintf("×%d", len(tu.group)), depth: 1, group: tu.group})
			for _, u := range m.sessionUnits(tu.group) {
				out = append(out, treeRow{kind: kindSession, s: u.group[0], name: short8(u.name), depth: 2, group: u.group})
			}
		}
	}
	return out
}

// visible is the filtered session list in table order, so an index into it is a selectable row.
func (m *model) visible() []*data.Session {
	var out []*data.Session
	for _, r := range m.rows() {
		if r.s != nil {
			out = append(out, r.s)
		}
	}
	return out
}

// shownGroups counts the repositories and tasks in the visible table.
func shownGroups(rows []treeRow) (repos, tasks int) {
	for _, r := range rows {
		switch r.kind {
		case kindRepo:
			repos++
		case kindTask:
			tasks++
		}
	}
	return
}

// cursorRow is the display row of the selected session, and the first row of
// the group rows directly above it (which stay in view with it).
func (m *model) cursorRow(rows []treeRow) (cur, first int) {
	found := false
	for i, r := range rows {
		if r.s == nil {
			continue
		}
		if !found {
			cur, found = i, true // a selection that is not shown falls back to the first session
		}
		if r.s.ID == m.selID {
			cur = i
			break
		}
	}
	first = cur
	for first > 0 && rows[first-1].s == nil {
		first--
	}
	return
}

func (m *model) cursor(vis []*data.Session) int {
	for i, s := range vis {
		if s.ID == m.selID {
			return i
		}
	}
	return 0
}

// mostRecent is the visible session with the latest activity.
func (m *model) mostRecent() *data.Session {
	var best *data.Session
	for _, s := range m.visible() {
		if best == nil || m.activity(s).After(m.activity(best)) {
			best = s
		}
	}
	return best
}

// normalizeSelection keeps selID valid for the current visible list.
func (m *model) normalizeSelection() {
	vis := m.visible()
	if len(vis) == 0 {
		m.selID = ""
		return
	}
	for _, s := range vis {
		if s.ID == m.selID {
			return
		}
	}
	m.selID = vis[0].ID
}

type totals struct {
	tokens, main, sub                 int64
	dispatches, repeats, loops, warns int
	rechecked, candidates, malformed  int
	byAgent                           map[string]data.Usage
	byModel                           map[string]data.Usage
}

func (m *model) totals() totals { return sumTotals(m.visible()) }

func sumTotals(ss []*data.Session) totals {
	t := totals{byAgent: map[string]data.Usage{}, byModel: map[string]data.Usage{}}
	for _, s := range ss {
		t.tokens += s.Tokens.Total
		t.main += s.Tokens.Main
		t.sub += s.Tokens.Subagents
		t.dispatches += s.DispatchCount
		t.repeats += s.Repeats
		t.loops += s.ReviewLoops
		t.warns += s.WarnCount
		t.malformed += s.Malformed
		t.rechecked += s.Recheck.Rechecked
		t.candidates += s.Recheck.ExplorersWithCitations
		for k, u := range s.ByAgent {
			v := t.byAgent[k]
			v.Add(u)
			t.byAgent[k] = v
		}
		for k, u := range s.ByModel {
			v := t.byModel[k]
			v.Add(u)
			t.byModel[k] = v
		}
	}
	return t
}

// retarget points every counter at the current values; jump skips the animation.
func (m *model) retarget(jump bool) {
	t := m.totals()
	set := func(k string, v float64) {
		if jump {
			m.counter[k].Jump(v)
		} else {
			m.counter[k].Set(v)
		}
	}
	set("tokens", float64(t.tokens))
	set("dispatches", float64(t.dispatches))
	set("repeats", float64(t.repeats))
	set("loops", float64(t.loops))
	set("warns", float64(t.warns))
	share := 0.0
	if t.tokens > 0 {
		share = 1000 * float64(t.sub) / float64(t.tokens)
	}
	set("share", share)
	rc := -1.0
	if t.candidates > 0 {
		rc = 1000 * float64(t.rechecked) / float64(t.candidates)
	}
	set("recheck", rc)
	for id, s := range m.sessions {
		c := m.rowTok[id]
		if c == nil {
			c = &Counter{}
			m.rowTok[id] = c
			if jump {
				c.Jump(float64(s.Tokens.Total))
			}
		}
		if jump {
			c.Jump(float64(s.Tokens.Total))
		} else {
			c.Set(float64(s.Tokens.Total))
		}
	}
}

// animating is true while anything is mid-motion (fast tick).
func (m *model) animating() bool {
	if m.loading && len(m.sessions) == 0 {
		return true
	}
	for _, c := range m.counter {
		if !c.Settled() {
			return true
		}
	}
	for _, c := range m.rowTok {
		if !c.Settled() {
			return true
		}
	}
	now := m.now()
	for _, t := range m.flash {
		if t.After(now) {
			return true
		}
	}
	if m.toast.text != "" && m.toast.until.After(now) {
		return true
	}
	switch m.view {
	case viewDashboard:
		return len(m.nowRunning()) > 0
	case viewSession:
		if d, s := m.details[m.selID], m.sessions[m.selID]; d != nil && s != nil && m.isLive(s) {
			for _, x := range d.Dispatches {
				if x.Status == "running" {
					return true
				}
			}
		}
	}
	return false
}

// running is a running dispatch of a live session.
type running struct {
	s *data.Session
	d data.Dispatch
}

// nowRunning lists every running dispatch of every live session, oldest first.
func (m *model) nowRunning() []running {
	var out []running
	for id, s := range m.sessions {
		det := m.details[id]
		if det == nil || !m.isLive(s) {
			continue
		}
		for _, x := range det.Dispatches {
			if x.Status == "running" && x.Start != nil {
				out = append(out, running{s, x})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].d, out[j].d
		if !a.Start.Equal(*b.Start) {
			return a.Start.Before(*b.Start)
		}
		return a.ToolUseID < b.ToolUseID
	})
	return out
}

// pulsing is true while a live dot is on screen.
func (m *model) pulsing() bool { return m.liveCount() > 0 }

// pulse is the bright half of the live-dot cycle.
func (m *model) pulse() bool {
	if m.opts.Static {
		return true
	}
	return (m.now().UnixMilli()/700)%2 == 0
}

// spinFrame is the current spinner frame.
func (m *model) spinFrame() int {
	if m.opts.Static {
		return 0
	}
	return int(m.now().UnixMilli() / 80)
}

// ---- merge / refresh -------------------------------------------------------------

func (m *model) setToast(text string, isErr bool) {
	m.toast = toast{text: text, err: isErr, until: m.now().Add(toastFor)}
}

func (m *model) inRange(s *data.Session) bool {
	since := ranges[m.rangeIdx].since
	if since == "" {
		return true
	}
	var d time.Duration
	switch since {
	case "24h":
		d = 24 * time.Hour
	case "7d":
		d = 7 * 24 * time.Hour
	case "30d":
		d = 30 * 24 * time.Hour
	}
	return m.now().Sub(m.activity(s)) <= d
}

// mergeSession folds a refreshed session into the list, the detail cache and the
// aggregates, raising toasts for events in sessions we have detail for. ns.LastChange
// says how new its data is; the list entry is never replaced by an older one.
func (m *model) mergeSession(ns *data.Session) {
	old := m.sessions[ns.ID]
	if old == nil && !m.inRange(ns) {
		return
	}
	if prev := m.details[ns.ID]; prev != nil && (m.follow || (m.view == viewSession && m.selID == ns.ID)) {
		m.diffToasts(prev, ns)
	}
	m.details[ns.ID] = ns
	if old != nil && old.LastChange.After(ns.LastChange) {
		return
	}
	summary := *ns
	summary.Dispatches = nil
	m.sessions[ns.ID] = &summary
	m.flash[ns.ID] = m.now().Add(flashFor)
	m.agentsDirty = true
	m.lastUpdate = m.now()
	if m.selID == "" {
		m.selID = ns.ID
	}
	m.retarget(false)
}

// mergeSeen folds the mtimes a load took into the poll baseline; afterwards seen is
// never nil.
func (m *model) mergeSeen(seen map[string]time.Time) {
	if m.seen == nil {
		m.seen = map[string]time.Time{}
	}
	for id, t := range seen {
		if t.After(m.seen[id]) {
			m.seen[id] = t
		}
	}
}

// mergeLoad folds a full load into the model. The load read its data after taking its
// mtimes, so a session whose data is newer than that (a per-session refresh landed
// meanwhile) is kept as it is.
func (m *model) mergeLoad(msg sessionsMsg) {
	next := map[string]*data.Session{}
	for i := range msg.res.Sessions {
		s := &msg.res.Sessions[i]
		if cur := m.sessions[s.ID]; cur != nil && cur.LastChange.After(s.LastChange) {
			s = cur
		}
		next[s.ID] = s
	}
	for id, cur := range m.sessions {
		_, loaded := next[id]
		if !loaded && m.exists(id) && msg.seen != nil && cur.LastChange.After(msg.seen[id]) {
			next[id] = cur // appeared after the load looked
		}
	}
	m.sessions = next
	m.pruneCaches()
}

// exists reports whether the session's transcript is known to be on disk.
func (m *model) exists(id string) bool {
	if m.seen == nil {
		return m.sessions[id] != nil
	}
	_, ok := m.seen[id]
	return ok
}

// pruneCaches forgets everything kept for sessions that left the list.
func (m *model) pruneCaches() {
	for id := range m.details {
		if m.sessions[id] == nil {
			delete(m.details, id)
		}
	}
	for id := range m.rowTok {
		if m.sessions[id] == nil {
			delete(m.rowTok, id)
		}
	}
	for id := range m.flash {
		if m.sessions[id] == nil {
			delete(m.flash, id)
		}
	}
}

// applyScan compares a poll with what was seen: changed and new sessions refresh,
// sessions whose transcript is gone are dropped.
func (m *model) applyScan(cur map[string]time.Time) []tea.Cmd {
	var cmds []tea.Cmd
	for id, t := range cur {
		if t.After(m.seen[id]) {
			m.seen[id] = t
			m.agentsDirty = true
			cmds = append(cmds, m.fetchSession(id))
		}
	}
	for id := range m.seen {
		if _, ok := cur[id]; !ok {
			delete(m.seen, id)
			delete(m.dirty, id)
			delete(m.retry, id)
			delete(m.sessions, id)
			m.agentsDirty = true
			if m.view == viewSession && m.selID == id {
				m.view = viewDashboard
			}
		}
	}
	m.pruneCaches()
	m.normalizeSelection()
	m.retarget(false)
	return cmds
}

// fetchLive makes sure every live session has its dispatches, for the Now running panel.
func (m *model) fetchLive() []tea.Cmd {
	var cmds []tea.Cmd
	for id, s := range m.sessions {
		if m.details[id] == nil && !m.inflight[id] && m.isLive(s) {
			cmds = append(cmds, m.fetchSession(id))
		}
	}
	return cmds
}

func (m *model) diffToasts(prev, cur *data.Session) {
	known := map[string]data.Dispatch{}
	for _, d := range prev.Dispatches {
		known[d.ToolUseID] = d
	}
	for _, d := range cur.Dispatches {
		was, seen := known[d.ToolUseID]
		switch {
		case !seen:
			m.setToast(fmt.Sprintf("▸ %s dispatched: %s", d.AgentType, firstLine(d.Description)), false)
		case was.Status == "running" && d.Status == "completed":
			dur := ""
			if x, ok := d.Duration(m.now()); ok {
				dur = " in " + FmtDuration(x)
			}
			m.setToast(fmt.Sprintf("✓ %s finished%s", d.AgentType, dur), false)
		case was.Status != "failed" && d.Status == "failed":
			m.setToast(fmt.Sprintf("✗ %s failed: %s", d.AgentType, firstLine(d.Description)), true)
		}
	}
	if cur.ReviewLoops > prev.ReviewLoops {
		m.setToast(fmt.Sprintf("↺ reviewer returned CHANGES_NEEDED (loop #%d)", cur.ReviewLoops), true)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func (m *model) followTarget() tea.Cmd {
	if !m.follow {
		return nil
	}
	s := m.mostRecent()
	if s == nil {
		return nil
	}
	m.selID = s.ID
	return m.ensureDetail()
}

// ---- update ------------------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.fixScroll()

	case sessionsMsg:
		if msg.gen != m.loadGen {
			break
		}
		m.loading, m.loaded = false, true
		m.mergeSeen(msg.seen)
		m.setScanErr(msg.scanErr)
		if msg.err != nil {
			m.noteFailure(msg.err)
			break
		}
		m.refreshErr = ""
		m.mergeLoad(msg)
		m.lastUpdate = m.now()
		m.normalizeSelection()
		m.retarget(false)
		cmds = append(cmds, m.followTarget())
		if m.view == viewSession {
			cmds = append(cmds, m.ensureDetail())
		}
		cmds = append(cmds, m.fetchLive()...)
		m.fixScroll()

	case agentsMsg:
		if msg.gen != m.loadGen {
			break
		}
		m.agentsBusy = false
		m.agentsAt = m.now()
		if msg.err != nil {
			m.agentsDirty = true // the next try waits for the next cadence
			m.noteFailure(msg.err)
			break
		}
		m.agents = msg.res
		m.agentsDirty = false

	case detailMsg:
		delete(m.inflight, msg.id)
		exists := m.exists(msg.id)
		switch {
		case msg.err != nil:
			if m.sessions[msg.id] != nil {
				m.noteFailure(msg.err)
				m.retry[msg.id] = m.now()
			}
		case exists:
			delete(m.retry, msg.id)
			m.refreshErr = ""
			msg.res.LastChange = msg.at
			m.mergeSession(msg.res)
			m.normalizeSelection()
			cmds = append(cmds, m.followTarget())
			m.fixScroll()
		}
		if m.dirty[msg.id] && exists {
			cmds = append(cmds, m.fetchSession(msg.id))
		}

	case scanMsg:
		m.scanning = false
		m.setScanErr(msg.err)
		if msg.err == nil {
			cmds = append(cmds, m.applyScan(msg.seen)...)
			m.fixScroll()
		}

	case tickMsg:
		if msg.gen != m.tickGen {
			break // a faster tick replaced this one
		}
		m.tickOn = false
		now := m.now()
		m.step(now.Sub(m.tickFrom))
		if m.loaded && !m.scanning && now.Sub(m.polledAt) >= pollEvery {
			m.polledAt = now
			cmds = append(cmds, m.scanCmd())
		}
		for id, at := range m.retry {
			if now.Sub(at) >= agentsEvery && !m.inflight[id] {
				cmds = append(cmds, m.fetchSession(id))
			}
		}
		if m.agentsDirty && !m.agentsBusy && now.Sub(m.agentsAt) >= agentsEvery {
			cmds = append(cmds, m.loadAgentsCmd())
		}

	case tea.KeyPressMsg:
		cmds = append(cmds, m.handleKey(msg))
	}
	cmds = append(cmds, m.ensureTick())
	return m, tea.Batch(cmds...)
}

// setScanErr records whether the last scan worked; a failing one marks the screen
// stale, with one toast when it starts failing.
func (m *model) setScanErr(err error) {
	if err == nil {
		m.scanErr = ""
		return
	}
	if m.scanErr == "" && len(m.sessions) > 0 {
		m.setToast("scan failed: "+firstLine(err.Error()), true)
	}
	m.scanErr = err.Error()
}

func (m *model) noteFailure(err error) {
	m.refreshErr = err.Error()
	if len(m.sessions) > 0 {
		m.setToast("refresh failed: "+firstLine(err.Error()), true)
	}
}

// step advances the tweens by dt and prunes finished flashes and toasts.
func (m *model) step(dt time.Duration) {
	for _, c := range m.counter {
		c.Step(dt)
	}
	for _, c := range m.rowTok {
		c.Step(dt)
	}
	now := m.now()
	for id, t := range m.flash {
		if !t.After(now) {
			delete(m.flash, id)
		}
	}
	if m.toast.text != "" && !m.toast.until.After(now) {
		m.toast = toast{}
	}
}

// settle jumps every animation to its end state (snapshots).
func (m *model) settle() {
	m.retarget(true)
	m.flash = map[string]time.Time{}
}

// ---- keys ----------------------------------------------------------------------------

func (m *model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.Interrupt):
		return tea.Quit
	case m.filterIn:
		return m.filterKey(msg)
	case m.help:
		if key.Matches(msg, keys.Help, keys.Back, keys.Open, keys.Quit) {
			m.help = false
		}
		return nil
	}
	switch {
	case key.Matches(msg, keys.Quit):
		return tea.Quit
	case key.Matches(msg, keys.Help):
		m.help = true
	case key.Matches(msg, keys.Next):
		return m.switchView(viewID((int(m.view) + 1) % 3))
	case key.Matches(msg, keys.Prev):
		return m.switchView(viewID((int(m.view) + 2) % 3))
	case key.Matches(msg, keys.Dash):
		return m.switchView(viewDashboard)
	case key.Matches(msg, keys.Sess):
		return m.switchView(viewSession)
	case key.Matches(msg, keys.Agents):
		return m.switchView(viewAgents)
	case key.Matches(msg, keys.Down):
		m.move(1)
	case key.Matches(msg, keys.Up):
		m.move(-1)
	case key.Matches(msg, keys.Top):
		m.moveTo(0)
	case key.Matches(msg, keys.Bottom):
		m.moveTo(1 << 30)
	case key.Matches(msg, keys.PageDown):
		m.move(max(1, m.pageSize()/2))
	case key.Matches(msg, keys.PageUp):
		m.move(-max(1, m.pageSize()/2))
	case key.Matches(msg, keys.Open):
		return m.enter()
	case key.Matches(msg, keys.Back):
		m.back()
	case key.Matches(msg, keys.Range):
		if len(m.sessions) == 0 && m.refreshErr != "" {
			return m.reload() // the error screen: try the same range again
		}
		m.rangeIdx = (m.rangeIdx + 1) % len(ranges)
		m.setToast("range: "+ranges[m.rangeIdx].label, false)
		return m.reload()
	case key.Matches(msg, keys.Follow):
		m.follow = !m.follow
		if m.follow {
			m.setToast("follow mode on: tracking the most recent session", false)
			return m.followTarget()
		}
		m.setToast("follow mode off", false)
	case key.Matches(msg, keys.Sort):
		m.sortBy = (m.sortBy + 1) % 3
		m.setToast("sort: "+m.sortBy.String(), false)
	case key.Matches(msg, keys.Filter):
		if m.view == viewDashboard {
			m.filterIn = true
		}
	}
	m.fixScroll()
	return nil
}

func (m *model) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch {
	case key.Matches(msg, keys.FilterClear):
		m.filter, m.filterIn = "", false
	case key.Matches(msg, keys.FilterKeep):
		m.filterIn = false
	case key.Matches(msg, keys.Erase):
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case key.Matches(msg, keys.Wipe):
		m.filter = ""
	default:
		if t := msg.Key().Text; t != "" {
			m.filter += t
		}
	}
	m.normalizeSelection()
	m.retarget(false)
	m.fixScroll()
	return nil
}

func (m *model) switchView(v viewID) tea.Cmd {
	if v == viewSession {
		if m.selID == "" || m.sessions[m.selID] == nil {
			m.setToast("no session selected", false)
			return nil
		}
		m.view = v
		m.dcursor, m.dtop = 0, 0
		m.fixScroll()
		return m.ensureDetail()
	}
	m.view = v
	m.fixScroll()
	return nil
}

// ensureDetail fetches the selected session's dispatches unless they are here or coming.
func (m *model) ensureDetail() tea.Cmd {
	if m.selID == "" || m.details[m.selID] != nil || m.inflight[m.selID] {
		return nil
	}
	return m.fetchSession(m.selID)
}

func (m *model) enter() tea.Cmd {
	switch m.view {
	case viewDashboard:
		if len(m.visible()) == 0 {
			return nil
		}
		return m.switchView(viewSession)
	case viewSession:
		m.detailOnly = !m.detailOnly
	}
	return nil
}

func (m *model) back() {
	switch {
	case m.view == viewSession && m.detailOnly:
		m.detailOnly = false
	case m.view != viewDashboard:
		m.view = viewDashboard
	case m.filter != "":
		m.filter = ""
		m.normalizeSelection()
		m.retarget(false)
	}
}

func (m *model) listLen() int {
	switch m.view {
	case viewDashboard:
		return len(m.visible())
	case viewSession:
		if d := m.details[m.selID]; d != nil {
			return len(d.Dispatches)
		}
	case viewAgents:
		if m.agents != nil {
			return len(m.agents.Agents)
		}
	}
	return 0
}

func (m *model) pageSize() int { return max(1, m.h-10) }

func (m *model) move(delta int) {
	n := m.listLen()
	if n == 0 {
		return
	}
	var cur int
	switch m.view {
	case viewDashboard:
		cur = m.cursor(m.visible())
	case viewSession:
		cur = m.dcursor
	case viewAgents:
		cur = m.acursor
	}
	m.moveTo(cur + delta)
}

func (m *model) moveTo(i int) {
	n := m.listLen()
	if n == 0 {
		return
	}
	i = max(0, min(n-1, i))
	switch m.view {
	case viewDashboard:
		m.selID = m.visible()[i].ID
		if m.follow {
			m.follow = false
			m.setToast("follow mode off", false)
		}
	case viewSession:
		m.dcursor = i
	case viewAgents:
		m.acursor = i
	}
	m.fixScroll()
}

// fixScroll keeps each cursor inside its window.
func (m *model) fixScroll() {
	keep := func(cursor, top, rows int) int {
		if rows < 1 {
			return 0
		}
		if cursor < top {
			top = cursor
		}
		if cursor >= top+rows {
			top = cursor - rows + 1
		}
		return max(0, top)
	}
	rows := m.rows()
	cur, first := m.cursorRow(rows)
	n := m.dashRows()
	if first < m.top {
		m.top = first
	}
	m.top = keep(cur, m.top, n)
	m.dtop = keep(m.dcursor, m.dtop, m.sessRows())
	m.atop = keep(m.acursor, m.atop, m.agentRows())
	m.top = min(m.top, max(0, len(rows)-n))
}
