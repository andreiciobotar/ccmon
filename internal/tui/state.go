package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ccmon/internal/data"
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
	liveWindow      = 30 * time.Second
	debounceDelay   = 400 * time.Millisecond
	debounceMax     = 2 * time.Second
	agentsEvery     = 20 * time.Second
	flashFor        = 1500 * time.Millisecond
	toastFor        = 4 * time.Second
	tickFast        = 80 * time.Millisecond
	tickSlow        = 400 * time.Millisecond
	tickIdle        = time.Second
	minWidth        = 60
	minHeight       = 16
	staleAfterFails = 1
)

// Options configures a model.
type Options struct {
	Runner  *data.Runner
	Range   string // initial range label, default 7d
	NoColor bool
	Follow  bool
	Changes <-chan data.Change // watcher events; nil disables live updates
	Static  bool               // render settled (snapshots, tests): no animation phase
	Now     func() time.Time
}

type toast struct {
	text  string
	err   bool
	until time.Time
}

// pending is the per-session refresh bookkeeping (debounce, one refresh in flight).
type pending struct {
	gen        int       // bumped on every change; a timer only fires when it carries the latest
	firstDirty time.Time // when the current burst began
	scheduled  bool
	inflight   bool
	dirty      bool // changed while a refresh was in flight
}

type model struct {
	opts   Options
	th     *Theme
	runner *data.Runner
	now    func() time.Time
	w, h   int

	view     viewID
	help     bool
	rangeIdx int
	sortBy   sortMode
	follow   bool
	filter   string
	filterIn bool // typing in the filter box

	sessions map[string]*data.Session
	details  map[string]*data.Session
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
	failures    int
	lastUpdate  time.Time
	agentsBusy  bool
	agentsDirty bool
	agentsAt    time.Time

	pend    map[string]*pending
	counter map[string]*Counter
	rowTok  map[string]*Counter
	flash   map[string]time.Time
	toast   toast

	tickOn bool
}

// New builds the model.
func New(o Options) *model {
	if o.Range == "" {
		o.Range = "7d"
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	idx := 1
	for i, r := range ranges {
		if r.label == o.Range {
			idx = i
		}
	}
	m := &model{
		opts: o, th: NewTheme(o.NoColor), runner: o.Runner, now: o.Now,
		w: 100, h: 30, rangeIdx: idx, follow: o.Follow, loading: true,
		sessions: map[string]*data.Session{}, details: map[string]*data.Session{},
		pend: map[string]*pending{}, counter: map[string]*Counter{},
		rowTok: map[string]*Counter{}, flash: map[string]time.Time{},
	}
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
	gen int
	res *data.SessionsResult
	err error
}
type agentsMsg struct {
	gen int
	res *data.Agents
	err error
}
type detailMsg struct {
	id  string
	res *data.Session
	err error
}
type changeMsg data.Change
type refreshDueMsg struct {
	id  string
	gen int
}
type tickMsg time.Time

// ---- commands ----------------------------------------------------------------

func (m *model) loadSessionsCmd() tea.Cmd {
	if m.runner == nil || m.fatal != nil {
		return nil
	}
	r, since, gen := m.runner, ranges[m.rangeIdx].since, m.loadGen
	return func() tea.Msg {
		res, err := r.Sessions(context.Background(), since)
		if err == nil {
			for i := range res.Sessions {
				if t := data.StatMtime(res.Sessions[i].Path); t > 0 {
					res.Sessions[i].LastChange = time.Unix(0, t)
				}
			}
		}
		return sessionsMsg{gen: gen, res: res, err: err}
	}
}

func (m *model) loadAgentsCmd() tea.Cmd {
	if m.runner == nil || m.fatal != nil {
		return nil
	}
	m.agentsBusy = true
	r, since, gen := m.runner, ranges[m.rangeIdx].since, m.loadGen
	return func() tea.Msg {
		res, err := r.Agents(context.Background(), since)
		return agentsMsg{gen: gen, res: res, err: err}
	}
}

func (m *model) fetchSessionCmd(id string) tea.Cmd {
	if m.runner == nil || m.fatal != nil {
		return nil
	}
	r := m.runner
	return func() tea.Msg {
		res, err := r.Session(context.Background(), id)
		return detailMsg{id: id, res: res, err: err}
	}
}

func waitChange(ch <-chan data.Change) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return nil
		}
		return changeMsg(c)
	}
}

func (m *model) ensureTick() tea.Cmd {
	if m.tickOn || m.opts.Static {
		return nil
	}
	m.tickOn = true
	d := tickIdle
	switch {
	case m.animating():
		d = tickFast
	case m.pulsing():
		d = tickSlow
	}
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Init starts the first load, the watcher loop and the animation clock.
func (m *model) Init() tea.Cmd {
	m.loadGen++
	return tea.Batch(m.loadSessionsCmd(), m.loadAgentsCmd(), waitChange(m.opts.Changes), m.ensureTick())
}

// ---- derived state -------------------------------------------------------------

func (m *model) activity(s *data.Session) time.Time {
	t := s.LastChange
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

// visible is the filtered, sorted session list.
func (m *model) visible() []*data.Session {
	f := strings.ToLower(strings.TrimSpace(m.filter))
	out := make([]*data.Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if f != "" && !strings.Contains(strings.ToLower(s.Project), f) && !strings.Contains(strings.ToLower(s.ID), f) {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch m.sortBy {
		case sortTokens:
			if a.Tokens.Total != b.Tokens.Total {
				return a.Tokens.Total > b.Tokens.Total
			}
		case sortShare:
			if a.Tokens.SubagentPct != b.Tokens.SubagentPct {
				return a.Tokens.SubagentPct > b.Tokens.SubagentPct
			}
		}
		ta, tb := m.activity(a), m.activity(b)
		if !ta.Equal(tb) {
			return ta.After(tb)
		}
		return a.ID < b.ID
	})
	return out
}

func (m *model) cursor(vis []*data.Session) int {
	for i, s := range vis {
		if s.ID == m.selID {
			return i
		}
	}
	return 0
}

// mostRecent is the session with the latest activity.
func (m *model) mostRecent() *data.Session {
	var best *data.Session
	for _, s := range m.sessions {
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
	rechecked, candidates             int
	byAgent                           map[string]data.Usage
	byModel                           map[string]data.Usage
}

func (m *model) totals() totals {
	t := totals{byAgent: map[string]data.Usage{}, byModel: map[string]data.Usage{}}
	for _, s := range m.visible() {
		t.tokens += s.Tokens.Total
		t.main += s.Tokens.Main
		t.sub += s.Tokens.Subagents
		t.dispatches += s.DispatchCount
		t.repeats += s.Repeats
		t.loops += s.ReviewLoops
		t.warns += s.WarnCount
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
	if m.view == viewSession {
		if d := m.details[m.selID]; d != nil {
			for _, x := range d.Dispatches {
				if x.Status == "running" {
					return true
				}
			}
		}
	}
	return false
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
// aggregates, raising toasts for events in sessions we have detail for.
func (m *model) mergeSession(ns *data.Session) {
	old := m.sessions[ns.ID]
	prev := m.details[ns.ID]
	if old != nil {
		ns.LastChange = old.LastChange
	}
	if t := data.StatMtime(ns.Path); t > 0 {
		if lt := time.Unix(0, t); lt.After(ns.LastChange) {
			ns.LastChange = lt
		}
	}
	if old == nil && !m.inRange(ns) {
		m.details[ns.ID] = ns
		return
	}
	if prev != nil && (m.follow || (m.view == viewSession && m.selID == ns.ID)) {
		m.diffToasts(prev, ns)
	}
	m.details[ns.ID] = ns
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

// noteChange does the debounce bookkeeping for a transcript change and returns the
// timer command when one needs scheduling.
func (m *model) noteChange(id string, at time.Time) tea.Cmd {
	p := m.pend[id]
	if p == nil {
		p = &pending{}
		m.pend[id] = p
	}
	if s := m.sessions[id]; s != nil && at.After(s.LastChange) {
		s.LastChange = at
	}
	m.agentsDirty = true
	p.gen++
	if p.inflight {
		p.dirty = true
		return nil
	}
	now := m.now()
	if !p.scheduled {
		p.firstDirty = now
	}
	p.scheduled = true
	delay := debounceDelay
	if left := debounceMax - now.Sub(p.firstDirty); left < delay {
		delay = max(left, 0)
	}
	id2, gen := id, p.gen
	return tea.Tick(delay, func(time.Time) tea.Msg { return refreshDueMsg{id: id2, gen: gen} })
}

// refreshDue starts the single refresh for a session, unless a newer timer exists.
func (m *model) refreshDue(msg refreshDueMsg) tea.Cmd {
	p := m.pend[msg.id]
	if p == nil || !p.scheduled || p.inflight {
		return nil
	}
	if msg.gen != p.gen && m.now().Sub(p.firstDirty) < debounceMax {
		return nil // a later change rescheduled the timer
	}
	p.scheduled = false
	p.inflight = true
	return m.fetchSessionCmd(msg.id)
}

// refreshDone clears the in-flight flag and schedules another pass when more changed.
func (m *model) refreshDone(id string) tea.Cmd {
	p := m.pend[id]
	if p == nil {
		return nil
	}
	p.inflight = false
	if p.dirty {
		p.dirty = false
		p.firstDirty = m.now()
		p.scheduled = true
		p.gen++
		id2, gen := id, p.gen
		return tea.Tick(debounceDelay, func(time.Time) tea.Msg { return refreshDueMsg{id: id2, gen: gen} })
	}
	return nil
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
	if m.details[s.ID] == nil {
		return m.fetchSessionCmd(s.ID)
	}
	return nil
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
		m.loading = false
		if msg.err != nil {
			m.noteFailure(msg.err)
			break
		}
		m.refreshErr, m.failures = "", 0
		first := len(m.sessions) == 0
		next := map[string]*data.Session{}
		for i := range msg.res.Sessions {
			s := msg.res.Sessions[i]
			next[s.ID] = &s
		}
		m.sessions = next
		m.lastUpdate = m.now()
		m.normalizeSelection()
		m.retarget(false)
		if first {
			// count up from zero on the first load only
		}
		if c := m.followTarget(); c != nil {
			cmds = append(cmds, c)
		}
		if m.view == viewSession && m.selID != "" {
			cmds = append(cmds, m.fetchSessionCmd(m.selID))
		}
		m.fixScroll()

	case agentsMsg:
		m.agentsBusy = false
		if msg.gen != m.loadGen {
			break
		}
		if msg.err != nil {
			m.noteFailure(msg.err)
			break
		}
		m.agents = msg.res
		m.agentsAt = m.now()
		m.agentsDirty = false

	case detailMsg:
		if c := m.refreshDone(msg.id); c != nil {
			cmds = append(cmds, c)
		}
		if msg.err != nil {
			if m.sessions[msg.id] != nil {
				m.noteFailure(msg.err)
			}
			break
		}
		m.refreshErr, m.failures = "", 0
		m.mergeSession(msg.res)
		m.normalizeSelection()
		if m.follow {
			if c := m.followTarget(); c != nil {
				cmds = append(cmds, c)
			}
		}
		m.fixScroll()

	case changeMsg:
		if c := m.noteChange(msg.SessionID, msg.At); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, waitChange(m.opts.Changes))

	case refreshDueMsg:
		if c := m.refreshDue(msg); c != nil {
			cmds = append(cmds, c)
		}

	case tickMsg:
		m.tickOn = false
		m.step(tickFast)
		if m.agentsDirty && !m.agentsBusy && m.now().Sub(m.agentsAt) >= agentsEvery {
			cmds = append(cmds, m.loadAgentsCmd())
		}

	case tea.KeyPressMsg:
		cmds = append(cmds, m.handleKey(msg))

	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			m.move(-1)
		case tea.MouseWheelDown:
			m.move(1)
		}
	}
	cmds = append(cmds, m.ensureTick())
	return m, tea.Batch(cmds...)
}

func (m *model) noteFailure(err error) {
	m.failures++
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
	k := msg.String()
	if k == "ctrl+c" {
		return tea.Quit
	}
	if m.filterIn {
		return m.filterKey(msg)
	}
	if m.help {
		switch k {
		case "?", "esc", "q", "enter", "backspace":
			m.help = false
		}
		return nil
	}
	switch k {
	case "q":
		return tea.Quit
	case "?":
		m.help = true
	case "tab":
		return m.switchView(viewID((int(m.view) + 1) % 3))
	case "shift+tab":
		return m.switchView(viewID((int(m.view) + 2) % 3))
	case "1":
		return m.switchView(viewDashboard)
	case "2":
		return m.switchView(viewSession)
	case "3":
		return m.switchView(viewAgents)
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "g", "home":
		m.moveTo(0)
	case "G", "end":
		m.moveTo(1 << 30)
	case "pgdown", "ctrl+d":
		m.move(max(1, m.pageSize()/2))
	case "pgup", "ctrl+u":
		m.move(-max(1, m.pageSize()/2))
	case "enter":
		return m.enter()
	case "esc", "backspace":
		m.back()
	case "r":
		m.rangeIdx = (m.rangeIdx + 1) % len(ranges)
		m.loadGen++
		m.loading = true
		m.setToast("range: "+ranges[m.rangeIdx].label, false)
		return tea.Batch(m.loadSessionsCmd(), m.loadAgentsCmd())
	case "f":
		m.follow = !m.follow
		if m.follow {
			m.setToast("follow mode on: tracking the most recent session", false)
			return m.followTarget()
		}
		m.setToast("follow mode off", false)
	case "s":
		m.sortBy = (m.sortBy + 1) % 3
		m.setToast("sort: "+m.sortBy.String(), false)
		m.fixScroll()
	case "/":
		if m.view == viewDashboard {
			m.filterIn = true
		}
	}
	m.fixScroll()
	return nil
}

func (m *model) filterKey(msg tea.KeyPressMsg) tea.Cmd {
	switch k := msg.String(); k {
	case "esc":
		m.filter, m.filterIn = "", false
	case "enter":
		m.filterIn = false
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
	case "ctrl+u":
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

func (m *model) ensureDetail() tea.Cmd {
	if m.selID == "" || m.details[m.selID] != nil {
		return nil
	}
	return m.fetchSessionCmd(m.selID)
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
	vis := m.visible()
	m.top = keep(m.cursor(vis), m.top, m.dashRows())
	m.dtop = keep(m.dcursor, m.dtop, m.sessRows())
	m.atop = keep(m.acursor, m.atop, m.agentRows())
	if n := len(vis); m.top > max(0, n-1) {
		m.top = 0
	}
}
