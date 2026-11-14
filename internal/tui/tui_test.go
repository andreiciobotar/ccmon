package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"ccmon/internal/data"
)

func TestMain(m *testing.M) {
	time.Local = time.UTC // rendered times must not depend on the machine
	os.Exit(m.Run())
}

// ---- fixtures ---------------------------------------------------------------------

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func usage(in, out, cw, cr int64) data.Usage {
	return data.Usage{Input: in, Output: out, CacheWrite: cw, CacheRead: cr, Total: in + out + cw + cr}
}

func fakeSession(id, project string, startMin int, main, sub int64) *data.Session {
	st := t0.Add(time.Duration(startMin) * time.Minute)
	en := st.Add(30 * time.Minute)
	tot := main + sub
	return &data.Session{
		ID: id, Project: project, Repo: project, Path: "/nonexistent/" + id + ".jsonl", Start: &st, End: &en,
		Tokens: data.Tokens{Total: tot, Main: main, Subagents: sub,
			MainPct: 100 * float64(main) / float64(tot), SubagentPct: 100 * float64(sub) / float64(tot)},
		ByModel:       map[string]data.Usage{"claude-fable-5-2": usage(1, 2, 3, main), "claude-haiku-5-5": usage(1, 2, 3, sub)},
		ByAgent:       map[string]data.Usage{"main": usage(1, 2, 3, main), "explorer": usage(1, 2, 3, sub/2), "coder": usage(1, 2, 3, sub/2)},
		DispatchCount: 3, Repeats: 1, ReviewLoops: 1, WarnCount: 1, Malformed: 2,
		Recheck:    data.Recheck{ExplorersWithCitations: 2, Rechecked: 1, Rate: ptr(0.5)},
		LastChange: en,
	}
}

func fakeDetail(s *data.Session) *data.Session {
	d := *s
	mk := func(i int, agent, status string, min, durMin float64, flags ...string) data.Dispatch {
		st := s.Start.Add(time.Duration(min * float64(time.Minute)))
		x := data.Dispatch{
			ToolUseID: fmt.Sprintf("toolu_%d", i), AgentID: fmt.Sprintf("a%d", i), AgentType: agent,
			Description: fmt.Sprintf("Task number %d with a rather long description that must be truncated 日本語", i),
			Depth:       1, Status: status, Start: &st, Models: []string{"claude-haiku-5-5"},
			Usage: usage(10, 200, 3000, 40000), UsageByModel: map[string]data.Usage{}, Flags: flags,
		}
		if status != "running" {
			x.DurationMS = ptr(durMin * 60000)
			r := st.Add(time.Duration(durMin * float64(time.Minute)))
			x.ReturnedAt = &r
		}
		if len(flags) > 0 && flags[0] == "WARN" {
			x.Warnings = []string{"agent explorer expected model haiku, used opus-5-5"}
		}
		return x
	}
	d.Dispatches = []data.Dispatch{
		mk(1, "explorer", "completed", 1, 3),
		mk(2, "coder", "completed", 5, 8, "review-loop", "repeat"),
		mk(3, "reviewer", "failed", 14, 2, "WARN"),
		mk(4, "my-custom-agent", "running", 20, 0),
		mk(5, "general-purpose", "no-run", 22, 0),
	}
	rep := "toolu_1"
	d.Dispatches[1].RepeatOf, d.Dispatches[1].RepeatScore = &rep, ptr(0.62)
	d.ReviewLoopEvent = []data.ReviewLoopEvent{{VerdictAt: &st0, Via: "dispatch", CoderAgentID: "a2"}}
	return &d
}

var st0 = t0.Add(10 * time.Minute)

func fakeAgents() *data.Agents {
	row := func(name string, n int) data.AgentRow {
		return data.AgentRow{Agent: name, Dispatches: n, Models: []string{"claude-haiku-5-5"}, Usage: usage(1, 2, 3, int64(1000*(n+1))),
			TokensP50: ptr(1000.0), TokensP90: ptr(5000.0), DurationP50: ptr(60000.0), DurationP90: ptr(300000.0), Repeats: n % 2, WarnCount: n % 3}
	}
	return &data.Agents{Agents: []data.AgentRow{row("main", 0), row("explorer", 4), row("coder", 2), row("odd-one", 1)},
		ByModel: map[string]data.Usage{"claude-haiku-5-5": usage(1, 2, 3, 4000), "claude-fable-5-2": usage(1, 2, 3, 9000)}, Sessions: 3}
}

func fakeModel(noColor bool, w, h int) *model {
	return newFake(Options{NoColor: noColor}, w, h)
}

// newFake is a static model holding three fake sessions, one with detail.
func newFake(o Options, w, h int) *model {
	o.Static = true
	o.Now = func() time.Time { return t0.Add(time.Hour) }
	m := New(o)
	m.w, m.h = w, h
	a, b, c := fakeSession("aaaaaaaa-0001", "alpha", 0, 800000, 2000000), fakeSession("bbbbbbbb-0002", "beta-é", 5, 100, 50), fakeSession("cccccccc-0003", "gamma", 10, 9000000, 1000)
	m.preload([]*data.Session{a, b, c}, []*data.Session{fakeDetail(a)}, fakeAgents())
	return m
}

func press(s string) tea.KeyPressMsg {
	switch s {
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

func send(m *model, keys ...string) {
	for _, k := range keys {
		m.Update(press(k))
	}
}

// ---- formatting / tween -----------------------------------------------------------------

func TestHuman(t *testing.T) {
	cases := map[float64]string{0: "0", 999: "999", 1000: "1.0k", 1234: "1.2k", 999999: "1000.0k", 1.5e6: "1.5M", 2.5e9: "2.5G", 12135912: "12.1M"}
	for in, want := range cases {
		if got := Human(in); got != want {
			t.Errorf("Human(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestFmtDuration(t *testing.T) {
	cases := map[time.Duration]string{0: "0s", 42 * time.Second: "42s", 90 * time.Second: "1m30s", 5 * time.Minute: "5m00s", 62 * time.Minute: "1h02m", -time.Second: "0s"}
	for in, want := range cases {
		if got := FmtDuration(in); got != want {
			t.Errorf("FmtDuration(%v) = %q, want %q", in, got, want)
		}
	}
	if FmtDurationMS(nil) != "-" {
		t.Error("nil duration should be -")
	}
}

func TestCounterTween(t *testing.T) {
	var c Counter
	c.Set(1000)
	prev := c.Cur
	steps := 0
	for c.Step(80 * time.Millisecond) {
		if c.Cur <= prev || c.Cur > 1000 {
			t.Fatalf("not monotonic towards target: %v after %v", c.Cur, prev)
		}
		prev = c.Cur
		steps++
		if steps > 200 {
			t.Fatal("did not settle")
		}
	}
	if !c.Settled() || c.Value() != 1000 {
		t.Fatalf("final %v", c.Cur)
	}
	if steps < 3 {
		t.Fatalf("should take several ticks, took %d", steps)
	}
	c.Set(10) // count down
	c.Step(80 * time.Millisecond)
	if c.Cur >= 1000 || c.Cur <= 10 {
		t.Fatalf("count down expected, at %v", c.Cur)
	}
	c.Jump(5)
	if !c.Settled() || c.Step(time.Second) {
		t.Fatal("jump should settle")
	}
}

func TestTruncByDisplayWidth(t *testing.T) {
	s := "日本語のテキスト"
	got := trunc(s, 7)
	if width(got) > 7 || !strings.HasSuffix(got, "…") {
		t.Fatalf("trunc = %q (%d cells)", got, width(got))
	}
	if cell("é", 3, true) != "  é" {
		t.Fatal("cell should pad by display width")
	}
	if trunc("abc", 3) != "abc" || trunc("abcd", 3) != "ab…" {
		t.Fatal("ascii truncation")
	}
}

// ---- keys ----------------------------------------------------------------------------------

func TestViewSwitching(t *testing.T) {
	m := fakeModel(true, 120, 40)
	send(m, "tab")
	if m.view != viewSession {
		t.Fatalf("tab -> %v", m.view)
	}
	send(m, "tab")
	if m.view != viewAgents {
		t.Fatalf("tab -> %v", m.view)
	}
	send(m, "tab")
	if m.view != viewDashboard {
		t.Fatalf("tab wraps -> %v", m.view)
	}
	send(m, "shift+tab")
	if m.view != viewAgents {
		t.Fatalf("shift+tab -> %v", m.view)
	}
	send(m, "1")
	if m.view != viewDashboard {
		t.Fatal("1")
	}
	send(m, "3")
	if m.view != viewAgents {
		t.Fatal("3")
	}
	send(m, "esc")
	if m.view != viewDashboard {
		t.Fatal("esc goes back to dashboard")
	}
	send(m, "enter")
	if m.view != viewSession {
		t.Fatal("enter opens session")
	}
	send(m, "backspace")
	if m.view != viewDashboard {
		t.Fatal("backspace goes back")
	}
	send(m, "?")
	if !m.help {
		t.Fatal("? opens help")
	}
	send(m, "?")
	if m.help {
		t.Fatal("? closes help")
	}
}

func TestQuitKeys(t *testing.T) {
	m := fakeModel(true, 100, 30)
	for _, k := range []string{"q", "ctrl+c"} {
		cmd := m.handleKey(press(k))
		if cmd == nil {
			t.Fatalf("%s should quit", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%s did not produce QuitMsg", k)
		}
	}
}

func TestCursorBounds(t *testing.T) {
	m := fakeModel(true, 120, 40)
	vis := m.visible()
	if m.cursor(vis) != 0 {
		t.Fatal("starts on first row")
	}
	send(m, "k", "up")
	if m.cursor(m.visible()) != 0 {
		t.Fatal("cannot go above the first row")
	}
	for i := 0; i < 10; i++ {
		send(m, "j")
	}
	if got := m.cursor(m.visible()); got != len(vis)-1 {
		t.Fatalf("cursor %d, want last %d", got, len(vis)-1)
	}
	send(m, "g")
	if m.cursor(m.visible()) != 0 {
		t.Fatal("g jumps to top")
	}
	send(m, "G")
	if m.cursor(m.visible()) != len(vis)-1 {
		t.Fatal("G jumps to bottom")
	}
	// session view cursor
	m.selID = "aaaaaaaa-0001"
	send(m, "enter")
	n := len(m.details[m.selID].Dispatches)
	for i := 0; i < 20; i++ {
		send(m, "down")
	}
	if m.dcursor != n-1 {
		t.Fatalf("dispatch cursor %d want %d", m.dcursor, n-1)
	}
	send(m, "g")
	if m.dcursor != 0 {
		t.Fatal("g in session view")
	}
}

func TestRangeCycle(t *testing.T) {
	m, _ := stubModel(t)
	start := m.rangeIdx
	gen := m.loadGen
	var seen []string
	for i := 0; i < len(ranges); i++ {
		send(m, "r")
		seen = append(seen, ranges[m.rangeIdx].label)
	}
	if m.rangeIdx != start {
		t.Fatalf("range should cycle back, at %d", m.rangeIdx)
	}
	if m.loadGen != gen+len(ranges) {
		t.Fatal("each range change must start a new load generation")
	}
	if strings.Join(seen, ",") != "30d,all,24h,7d" {
		t.Fatalf("cycle order %v", seen)
	}
}

func TestStaleLoadIgnored(t *testing.T) {
	m, _ := stubModel(t)
	send(m, "r")
	old := m.loadGen - 1
	n := len(m.sessions)
	m.Update(sessionsMsg{gen: old, res: &data.SessionsResult{}})
	if len(m.sessions) != n {
		t.Fatal("results of a superseded load must be dropped")
	}
	m.Update(sessionsMsg{gen: m.loadGen, res: &data.SessionsResult{}})
	if len(m.sessions) != 0 {
		t.Fatal("current load should apply")
	}
}

func TestSortCycle(t *testing.T) {
	m := fakeModel(true, 120, 40)
	if m.visible()[0].ID != "cccccccc-0003" { // latest activity
		t.Fatalf("recent first, got %s", m.visible()[0].ID)
	}
	send(m, "s")
	if m.sortBy != sortTokens || m.visible()[0].ID != "cccccccc-0003" {
		t.Fatalf("tokens sort: %v %s", m.sortBy, m.visible()[0].ID)
	}
	send(m, "s")
	if m.sortBy != sortShare || m.visible()[0].ID != "aaaaaaaa-0001" && m.visible()[0].ID != "bbbbbbbb-0002" {
		t.Fatalf("share sort: %v %s", m.sortBy, m.visible()[0].ID)
	}
	if m.visible()[0].ID != "aaaaaaaa-0001" { // 70% sub vs 33% vs ~0
		// b has 50/150 = 33% sub; a has 2M/2.8M = 71%
		t.Fatalf("highest subagent share first, got %s", m.visible()[0].ID)
	}
	send(m, "s")
	if m.sortBy != sortRecent {
		t.Fatal("sort wraps to recent")
	}
}

func TestFilter(t *testing.T) {
	m := fakeModel(true, 120, 40)
	send(m, "/")
	if !m.filterIn {
		t.Fatal("/ starts filtering")
	}
	send(m, "b", "e", "t")
	if got := len(m.visible()); got != 1 || m.visible()[0].Project != "beta-é" {
		t.Fatalf("filter result %d", got)
	}
	if m.selID != "bbbbbbbb-0002" {
		t.Fatal("selection must follow the filtered list")
	}
	send(m, "backspace", "backspace", "backspace")
	if len(m.visible()) != 3 {
		t.Fatal("empty filter shows everything")
	}
	send(m, "c", "c", "c", "c", "c", "c", "c", "c")
	if len(m.visible()) != 1 {
		t.Fatal("filter by id prefix")
	}
	send(m, "enter")
	if m.filterIn || m.filter == "" {
		t.Fatal("enter keeps the filter, leaves input mode")
	}
	send(m, "esc")
	if m.filter != "" || len(m.visible()) != 3 {
		t.Fatal("esc clears the filter")
	}
	send(m, "/", "z", "z", "z")
	if len(m.visible()) != 0 {
		t.Fatal("no match")
	}
	_ = m.Render() // empty-filter state renders
	send(m, "esc")
	if m.filterIn || m.filter != "" {
		t.Fatal("esc in input mode cancels")
	}
}

func TestFollowMode(t *testing.T) {
	m := fakeModel(true, 120, 40)
	send(m, "g")
	m.selID = "aaaaaaaa-0001"
	send(m, "f")
	if !m.follow || m.selID != "cccccccc-0003" {
		t.Fatalf("follow selects most recent: follow=%v sel=%s", m.follow, m.selID)
	}
	// a session gets newer activity
	m.seen["bbbbbbbb-0002"] = t0.Add(5 * time.Hour)
	m.followTarget()
	if m.selID != "bbbbbbbb-0002" {
		t.Fatalf("follow tracks the newest session, got %s", m.selID)
	}
	send(m, "j")
	if m.follow {
		t.Fatal("manual movement ends follow mode")
	}
	send(m, "f", "f")
	if m.follow {
		t.Fatal("f toggles")
	}
}

func TestLiveDetection(t *testing.T) {
	m := fakeModel(true, 100, 30)
	s := m.sessions["aaaaaaaa-0001"]
	m.seen[s.ID] = m.now().Add(-5 * time.Second)
	s.End = ptr(m.now().Add(-time.Hour))
	if !m.isLive(s) {
		t.Fatal("changed 5s ago is live")
	}
	m.seen[s.ID] = m.now().Add(-31 * time.Second)
	if m.isLive(s) {
		t.Fatal("31s ago is not live")
	}
}

// ---- merge -----------------------------------------------------------------------

func TestMergeRefreshedSession(t *testing.T) {
	m := fakeModel(true, 120, 40)
	m.follow = true
	old := m.sessions["aaaaaaaa-0001"]
	ns := fakeDetail(old)
	ns.Tokens.Total += 5000000
	ns.Tokens.Subagents += 5000000
	ns.DispatchCount = 4
	extra := ns.Dispatches[0]
	extra.ToolUseID, extra.AgentType, extra.Description = "toolu_new", "coder", "Brand new work"
	ns.Dispatches = append(ns.Dispatches, extra)
	m.mergeSession(ns)
	got := m.sessions["aaaaaaaa-0001"]
	if got.Tokens.Total != ns.Tokens.Total || got.DispatchCount != 4 {
		t.Fatal("summary not replaced")
	}
	if got.Dispatches != nil {
		t.Fatal("list entries stay summaries; detail lives in the cache")
	}
	if len(m.details["aaaaaaaa-0001"].Dispatches) != 6 {
		t.Fatal("detail cache not updated")
	}
	if _, ok := m.flash["aaaaaaaa-0001"]; !ok {
		t.Fatal("updated row should flash")
	}
	if !strings.Contains(m.toast.text, "coder dispatched") {
		t.Fatalf("expected a dispatch toast in follow mode, got %q", m.toast.text)
	}
	if m.counter["tokens"].Target != float64(m.totals().tokens) || m.counter["tokens"].Settled() {
		t.Fatal("KPI counter should ease to the new total")
	}
	if !m.agentsDirty {
		t.Fatal("aggregates should be marked for refresh")
	}
	// review loop verdict toast
	ns2 := fakeDetail(ns)
	ns2.Dispatches = ns.Dispatches
	ns2.ReviewLoops = ns.ReviewLoops + 1
	m.mergeSession(ns2)
	if !strings.Contains(m.toast.text, "CHANGES_NEEDED") || !m.toast.err {
		t.Fatalf("expected review loop toast, got %q", m.toast.text)
	}
	// a new session appears in range
	nw := fakeSession("dddddddd-0004", "delta", 50, 10, 10)
	nw.LastChange = m.now()
	m.mergeSession(nw)
	if _, ok := m.sessions["dddddddd-0004"]; !ok {
		t.Fatal("new session should be inserted")
	}
}

// ---- rendering --------------------------------------------------------------------------------

var sizes = [][2]int{{60, 16}, {80, 24}, {100, 30}, {140, 40}, {200, 60}, {120, 34}, {71, 19}}

func checkFrame(t *testing.T, name string, m *model, noColor bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: panic %v", name, r)
		}
	}()
	lines := m.frame()
	if len(lines) != m.h {
		t.Fatalf("%s: %d lines, want %d", name, len(lines), m.h)
	}
	for i, l := range lines {
		if w := width(l); w != m.w {
			t.Fatalf("%s: line %d is %d cells, want %d: %q", name, i, w, m.w, ansi.Strip(l))
		}
		if strings.ContainsAny(l, "\n\r") {
			t.Fatalf("%s: line %d has a newline", name, i)
		}
		if noColor && strings.Contains(l, "\x1b") {
			t.Fatalf("%s: no-color line %d has escapes: %q", name, i, l)
		}
	}
}

func TestRenderAllViewsAllSizes(t *testing.T) {
	for _, sz := range sizes {
		for _, nc := range []bool{false, true} {
			for _, view := range []string{"dashboard", "session", "agents", "help", "detail", "filter", "follow"} {
				m := fakeModel(nc, sz[0], sz[1])
				switch view {
				case "session":
					m.view = viewSession
				case "agents":
					m.view = viewAgents
				case "help":
					m.help = true
				case "detail":
					m.view, m.detailOnly = viewSession, true
				case "filter":
					m.filter, m.filterIn = "alp", true
				case "follow":
					m.follow = true
					m.toast = toast{text: "✓ explorer finished in 1m30s with a very long toast message that must be clipped", until: m.now().Add(time.Hour)}
				}
				m.selID = "aaaaaaaa-0001"
				m.fixScroll()
				name := fmt.Sprintf("%s %dx%d nc=%v", view, sz[0], sz[1], nc)
				checkFrame(t, name, m, nc)
			}
		}
	}
}

func TestRenderEmptyAndStates(t *testing.T) {
	for _, sz := range sizes {
		for _, v := range []viewID{viewDashboard, viewSession, viewAgents} {
			// empty data
			m := New(Options{Static: true})
			m.w, m.h = sz[0], sz[1]
			m.loading = false
			m.view = v
			checkFrame(t, "empty", m, false)
			// loading
			m = New(Options{Static: true})
			m.w, m.h = sz[0], sz[1]
			m.view = v
			checkFrame(t, "loading", m, false)
			// error with nothing loaded
			m.loading, m.refreshErr = false, "ccmon: boom\nsecond line with a very long text "+strings.Repeat("x", 200)
			checkFrame(t, "error", m, false)
			// fatal
			m.fatal = data.ErrPython
			checkFrame(t, "fatal", m, false)
			// stale data on screen
			m = fakeModel(false, sz[0], sz[1])
			m.view = v
			m.refreshErr = "boom"
			checkFrame(t, "stale", m, false)
			// session view without detail yet / unknown agent
			m.selID = "bbbbbbbb-0002"
			m.view = viewSession
			checkFrame(t, "nodetail", m, false)
			// agents not loaded yet
			m.agents = nil
			m.view = viewAgents
			checkFrame(t, "noagents", m, false)
		}
	}
}

func TestRenderWideRunesAndLongNames(t *testing.T) {
	m := fakeModel(false, 100, 30)
	m.sessions["aaaaaaaa-0001"].Project = strings.Repeat("日本語プロジェクト", 5)
	m.details["aaaaaaaa-0001"].Dispatches[0].Description = strings.Repeat("🙂漢字 emoji ", 30)
	m.selID = "aaaaaaaa-0001"
	for _, v := range []viewID{viewDashboard, viewSession, viewAgents} {
		m.view = v
		checkFrame(t, "wide", m, false)
	}
}

func TestSmallWindowMessage(t *testing.T) {
	m := fakeModel(true, 40, 10)
	out := ansi.Strip(m.Render())
	if !strings.Contains(out, "larger") {
		t.Fatalf("expected a friendly message, got:\n%s", out)
	}
	checkFrame(t, "small", m, true)
	for _, sz := range [][2]int{{1, 1}, {10, 3}, {59, 40}, {200, 5}} {
		m = fakeModel(false, sz[0], sz[1])
		checkFrame(t, "tiny", m, false)
	}
}

func TestNoColorHasNoEscapesAndColorDoes(t *testing.T) {
	if strings.Contains(fakeModel(true, 100, 30).Render(), "\x1b") {
		t.Fatal("no-color render has escapes")
	}
	c := fakeModel(false, 100, 30).Render()
	if !strings.Contains(c, "38;2;") || !strings.Contains(c, "48;2;18;17;28") {
		t.Fatal("color render should use truecolor on the theme background")
	}
}

func TestEveryCellHasBackground(t *testing.T) {
	// no unpainted seams: after any reset, a background must be set before the next
	// printable character.
	for _, v := range []viewID{viewDashboard, viewSession, viewAgents} {
		m := fakeModel(false, 120, 36)
		m.view = v
		for y, l := range m.frame() {
			hasBg := false
			i := 0
			for i < len(l) {
				if l[i] == 0x1b && i+1 < len(l) && l[i+1] == '[' {
					j := i + 2
					for j < len(l) && (l[j] < 0x40 || l[j] > 0x7e) {
						j++
					}
					params := l[i+2 : j]
					if j < len(l) && l[j] == 'm' {
						switch {
						case params == "" || params == "0":
							hasBg = false
						case strings.Contains(params, "48;2;"):
							hasBg = true
						}
					}
					i = j + 1
					continue
				}
				if !hasBg {
					t.Fatalf("view %d line %d: printable %q without a background at byte %d: %q", v, y, string(l[i]), i, l)
				}
				i++
			}
		}
	}
}

func TestGradientAndAgentColors(t *testing.T) {
	th := NewTheme(false)
	if key24(th.AgentColor("main")) != key24(th.Yellow) || key24(th.AgentColor("explorer")) != key24(th.Teal) ||
		key24(th.AgentColor("coder")) != key24(th.Blue) || key24(th.AgentColor("reviewer")) != key24(th.Pink) {
		t.Fatal("fixed agent colors")
	}
	if key24(th.AgentColor("factory:coder")) != key24(th.Blue) || key24(th.AgentColor("factory:reviewer")) != key24(th.Pink) {
		t.Fatal("a plugin's agent has the color of its role")
	}
	if key24(th.AgentColor("mystery")) != key24(th.AgentColor("mystery")) {
		t.Fatal("unknown agent colors must be stable")
	}
	if key24(th.gradientAt(0)) != key24(th.Blue) || key24(th.gradientAt(1)) != key24(th.Pink) || key24(th.gradientAt(0.5)) != key24(th.Purple) {
		t.Fatal("gradient endpoints blue -> purple -> pink")
	}
}

func TestStackSumsToWidth(t *testing.T) {
	th := NewTheme(true)
	p := th.on(th.Surface)
	for w := 1; w < 40; w++ {
		got := p.stack(w, []seg{{1, th.Blue}, {1, th.Pink}, {1, th.Teal}})
		if width(got) != w {
			t.Fatalf("stack width %d != %d", width(got), w)
		}
	}
	if width(p.stack(10, nil)) != 10 || width(p.bar(10, 0.33, th.Blue, true)) != 10 {
		t.Fatal("bar widths")
	}
}

func TestTickOnlyWhenNeeded(t *testing.T) {
	m := New(Options{Now: func() time.Time { return t0 }})
	m.loading = false
	if m.animating() {
		t.Fatal("empty idle model should not animate")
	}
	m.counter["tokens"].Set(100)
	if !m.animating() {
		t.Fatal("moving counter animates")
	}
	for i := 0; i < 100; i++ {
		m.step(tickFast)
	}
	if m.animating() {
		t.Fatal("settled counters stop the fast tick")
	}
}

// ---- grouped table ---------------------------------------------------------------------------

func groupSession(id, repo, path, task string, startMin int, main, sub int64) *data.Session {
	s := fakeSession(id, repo, startMin, main, sub)
	s.Repo, s.RepoPath, s.Task = repo, path, task
	return s
}

func modelOf(w, h int, ss ...*data.Session) *model {
	m := newFake(Options{NoColor: true}, w, h)
	m.sessions = map[string]*data.Session{}
	m.preload(ss, nil, fakeAgents())
	return m
}

// groupedModel holds: repo alpha (a1 without a task, task ci with a2 and a3, task zz with a4) and repo beta (b1).
func groupedModel(w, h int) *model {
	mk := func(id, repo, task string, startMin int, main, sub int64) *data.Session {
		return groupSession(id, repo, "/r/"+repo, task, startMin, main, sub)
	}
	return modelOf(w, h,
		mk("a1000000", "alpha", "", 0, 100, 0),
		mk("a2000000", "alpha", "ci", 10, 100, 100),
		mk("a3000000", "alpha", "ci", 20, 100, 100),
		mk("a4000000", "alpha", "zz", 30, 5000, 0),
		mk("b1000000", "beta", "", 40, 300, 0))
}

func rowNames(m *model) string {
	var out []string
	for _, r := range m.rows() {
		out = append(out, strings.Repeat("  ", r.depth)+strings.TrimSpace(r.name+" "+r.suffix))
	}
	return strings.Join(out, ",")
}

func TestTreeOrderUnderEachSort(t *testing.T) {
	m := groupedModel(120, 40)
	for _, c := range []struct {
		sort sortMode
		want string
	}{
		// recent: beta ends last; in alpha the sessions without a task come first, then tasks by latest activity
		{sortRecent, "beta b1000000,alpha ×4,  a1000000,  zz a4000000,  ci ×2,    a3000000,    a2000000"},
		// tokens: alpha 5.4k before beta 300; tasks by summed tokens, zz 5000 before ci 400
		{sortTokens, "alpha ×4,  a1000000,  zz a4000000,  ci ×2,    a3000000,    a2000000,beta b1000000"},
		// subagent share: ci 50% first among tasks; repositories by summed share (alpha ~3.6%, beta 0)
		{sortShare, "alpha ×4,  a1000000,  ci ×2,    a3000000,    a2000000,  zz a4000000,beta b1000000"},
	} {
		m.sortBy = c.sort
		if got := rowNames(m); got != c.want {
			t.Errorf("sort %v:\n got %s\nwant %s", c.sort, got, c.want)
		}
	}
}

func TestCursorSkipsGroupRows(t *testing.T) {
	m := groupedModel(120, 40)
	m.sortBy = sortTokens
	send(m, "g")
	var seen []string
	for i := 0; i < 6; i++ {
		seen = append(seen, short8(m.selID))
		send(m, "j")
	}
	if got, want := strings.Join(seen, ","), "a1000000,a4000000,a3000000,a2000000,b1000000,b1000000"; got != want {
		t.Fatalf("j order %s, want %s", got, want)
	}
	send(m, "k")
	if m.selID != "a2000000" {
		t.Fatalf("k -> %s", m.selID)
	}
	send(m, "G")
	if m.selID != "b1000000" {
		t.Fatalf("G -> %s", m.selID)
	}
}

func TestScrollKeepsGroupRowsAboveTheFirstSession(t *testing.T) {
	m := groupedModel(120, 15)
	m.sortBy = sortTokens
	if rows := m.dashRows(); rows >= len(m.rows()) {
		t.Fatalf("table shows all %d of %d rows; the test needs scrolling", rows, len(m.rows()))
	}
	send(m, "G", "k", "k") // a2, a3: first session of task ci
	if m.selID != "a3000000" {
		t.Fatalf("selected %s", m.selID)
	}
	if m.rows()[m.top].s != nil {
		t.Fatalf("top row %d is a session; the task row above a3000000 must stay in view", m.top)
	}
	send(m, "k", "k") // a4, a1: first session of the repository
	if m.selID != "a1000000" || m.rows()[m.top].s != nil {
		t.Fatalf("a1000000 must keep its repository row in view (top %d)", m.top)
	}
}

func TestFilterMatchesLabel(t *testing.T) {
	m := groupedModel(120, 40)
	send(m, "/", "a", "l", "p", "h", "a", "/", "c", "i")
	if got := rowNames(m); got != "alpha ×2,  ci ×2,    a3000000,    a2000000" {
		t.Fatalf("filter alpha/ci -> %s", got)
	}
	if got := m.totals().tokens; got != 400 {
		t.Fatalf("totals follow the filtered task: %d", got)
	}
}

func TestGroupedRenderFitsEverySize(t *testing.T) {
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {100, 30}, {140, 40}} {
		m := groupedModel(sz[0], sz[1])
		m.fixScroll()
		for i, line := range strings.Split(m.Render(), "\n") {
			if width(line) > sz[0] {
				t.Fatalf("%dx%d line %d is %d wide", sz[0], sz[1], i, width(line))
			}
		}
	}
}

func TestSortUsesGroupSums(t *testing.T) {
	m := modelOf(120, 40,
		groupSession("p1000000", "r", "/r", "p", 10, 300, 0),
		groupSession("p2000000", "r", "/r", "p", 20, 300, 0),
		groupSession("q1000000", "r", "/r", "q", 30, 500, 0))
	m.sortBy = sortTokens
	if got, want := rowNames(m), "r ×3,  p ×2,    p2000000,    p1000000,  q q1000000"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestSameNameRepositoriesStaySeparate(t *testing.T) {
	m := modelOf(120, 40,
		groupSession("x1000000", "app", "/one/app", "", 10, 100, 0),
		groupSession("y1000000", "app", "/two/app", "", 20, 100, 0))
	if got, want := rowNames(m), "app y1000000,app x1000000"; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestHiddenSelectionNeverSelectsAGroupRow(t *testing.T) {
	m := groupedModel(120, 40)
	m.selID = "a2000000"
	m.filter = "beta"
	rows := m.rows()
	if cur, _ := m.cursorRow(rows); rows[cur].s == nil {
		t.Fatalf("row %d is a group row", cur)
	}
	m.follow = true
	m.followTarget()
	if m.selID != "b1000000" {
		t.Fatalf("follow picked %s from outside the filter", m.selID)
	}
}

func TestScrollPullsBackWhenTheWindowGrows(t *testing.T) {
	m := groupedModel(120, 15)
	m.sortBy = sortTokens
	send(m, "G")
	if m.top == 0 {
		t.Fatal("the test needs a scrolled table")
	}
	m.h = 60
	m.fixScroll()
	if m.top != 0 {
		t.Fatalf("top %d after growing: group rows above are hidden", m.top)
	}
}
