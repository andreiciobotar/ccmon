package tui

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"ccmon/internal/data"
)

func plain(m *model) string { return ansi.Strip(m.Render()) }

func TestMixesAreAlwaysVisible(t *testing.T) {
	for _, sz := range [][2]int{{60, 16}, {80, 24}, {99, 30}, {100, 30}, {123, 33}, {140, 40}} {
		out := plain(fakeModel(true, sz[0], sz[1]))
		agents := strings.Contains(out, "Agent mix") || strings.Contains(out, "agents ")
		models := strings.Contains(out, "Model mix") || strings.Contains(out, "models ")
		if !agents || !models {
			t.Errorf("%dx%d: agent mix visible=%v, model mix visible=%v\n%s", sz[0], sz[1], agents, models, out)
		}
	}
}

func TestTimeColumnFollowsSort(t *testing.T) {
	m := fakeModel(true, 140, 40)
	if out := plain(m); !strings.Contains(out, "ACTIVE") || strings.Contains(out, "STARTED") {
		t.Fatal("sorted by recent, the column is ACTIVE")
	}
	send(m, "s")
	if out := plain(m); !strings.Contains(out, "STARTED") || strings.Contains(out, "ACTIVE") {
		t.Fatal("sorted otherwise, the column is STARTED")
	}
	// the cell shows start for session a (t0 10:00) rather than its activity (10:30)
	if !strings.Contains(plain(m), "11-13 10:00") {
		t.Fatal("STARTED shows the start time")
	}
}

func TestNowRunning(t *testing.T) {
	m := fakeModel(true, 140, 40)
	if out := plain(m); !strings.Contains(out, "Now running") || !strings.Contains(out, "nothing running") {
		t.Fatal("empty state")
	}
	m.seen[sidA] = m.now().Add(-5 * time.Second) // live
	out := plain(m)
	if !strings.Contains(out, "my-custom-agent") || strings.Contains(out, "nothing running") {
		t.Fatalf("a running dispatch of a live session is listed:\n%s", out)
	}
	if !m.animating() {
		t.Fatal("a running dispatch on screen animates")
	}
	m.view, m.selID = viewSession, sidA
	if out := plain(m); !strings.Contains(out, "running") || strings.Contains(out, "unfinished") {
		t.Fatal("live session: running")
	}
	m.seen[sidA] = t0 // no longer live
	m.view = viewSession
	if out := plain(m); !strings.Contains(out, "unfinished") {
		t.Fatalf("a running dispatch in a stale session is labelled unfinished:\n%s", out)
	}
	if m.animating() {
		t.Fatal("unfinished dispatches do not animate")
	}
}

func dispatchAt(id, agent string, startMin, durMin float64) data.Dispatch {
	st := t0.Add(time.Duration(startMin * float64(time.Minute)))
	return data.Dispatch{ToolUseID: id, AgentID: id, AgentType: agent, Status: "completed", Start: &st, DurationMS: ptr(durMin * 60000)}
}

func TestTimelineSubLanes(t *testing.T) {
	m := fakeModel(true, 140, 40)
	s := m.sessions[sidA]
	d := &data.Session{ID: sidA, Dispatches: []data.Dispatch{
		dispatchAt("e1", "explorer", 1, 10),
		dispatchAt("e2", "explorer", 3, 10), // overlaps e1
		dispatchAt("e3", "explorer", 20, 2), // fits after e1
		dispatchAt("c1", "coder", 5, 3),
	}}
	pl := m.timelinePlan(s, d, 140, 8, 0)
	if len(pl.lanes) != 3 || pl.lanes[0].agent != "coder" || len(pl.lanes[1].items) != 2 || len(pl.lanes[2].items) != 1 || pl.lanes[2].agent != "explorer" || !pl.lanes[1].first || pl.lanes[2].first {
		t.Fatalf("lanes: %+v", pl.lanes)
	}
	if pl.boxH() != 3+2+1 {
		t.Fatalf("rows are not padded: box height %d", pl.boxH())
	}
	if pl2 := m.timelinePlan(s, d, 140, 2, 0); len(pl2.lanes) != 1 || pl2.hidden != 2 {
		t.Fatalf("a capped timeline says what it hides: %+v", pl2)
	}
}

func TestDispatchRowsAlignWithAndWithoutFlags(t *testing.T) {
	m := fakeModel(true, 140, 40)
	m.view, m.selID = viewSession, sidA
	var head string
	col := -1
	for _, l := range strings.Split(plain(m), "\n") {
		if strings.Contains(l, "DESCRIPTION") {
			head = l
			col = len([]rune(l[:strings.Index(l, "DESCRIPTION")]))
			continue
		}
		if i := strings.Index(l, "Task number"); head != "" && i >= 0 {
			if got := len([]rune(l[:i])); got != col {
				t.Fatalf("description starts at cell %d, header says %d: %q", got, col, l)
			}
		}
	}
	if col < 0 {
		t.Fatal("no dispatch list found")
	}
	for _, h := range []string{"MODEL", "IN", "OUT", "TOTAL", "DUR", "FLAGS"} {
		if !strings.Contains(head, h) {
			t.Fatalf("column %s missing at 140 columns: %q", h, head)
		}
	}
}

func TestSessionHeaderSignalsAndNoSignalsBox(t *testing.T) {
	m := fakeModel(true, 140, 40)
	m.view, m.selID = viewSession, sidA
	out := plain(m)
	if strings.Contains(out, "Signals") {
		t.Fatal("the Signals box is gone")
	}
	for _, want := range []string{"repeats 1", "review loops 1", "warnings 1", "re-check 50%", "malformed 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("signals line lacks %q", want)
		}
	}
	// the review-loop event shows in the detail of the coder it names
	send(m, "down")
	if out := plain(m); !strings.Contains(out, "CHANGES_NEEDED") {
		t.Fatalf("detail of dispatch 2 lacks its review-loop event:\n%s", out)
	}
}

func TestMultiDaySpanShowsDates(t *testing.T) {
	m := fakeModel(true, 140, 40)
	s := m.sessions[sidA]
	end := s.Start.Add(30 * time.Hour)
	s.End = &end
	d := m.details[sidA]
	d.Dispatches = append(d.Dispatches, dispatchAt("late", "coder", 26*60, 120)) // the next day
	m.view, m.selID = viewSession, sidA
	out := plain(m)
	if !strings.Contains(out, "→ Nov 14 16:00") {
		t.Fatalf("header span lacks the end date:\n%s", out)
	}
	if !strings.Contains(out, "11-13 10:01") || !strings.Contains(out, "11-14 12:00") || !strings.Contains(out, "11-14 14:00") {
		t.Fatalf("timeline axis lacks dates:\n%s", out)
	}
}

func TestScrollThumbKeepsTheBorder(t *testing.T) {
	m := fakeModel(false, 100, 30)
	p := m.th.on(m.th.Surface)
	box := p.boxed("t", []string{"a", "b", "c", "d", "e", "f"}, 20, 8, m.th.Pink)
	box = m.scrollMark(p, box, 0, 3, 9)
	thumbs := 0
	for i, l := range box {
		if width(l) != 20 {
			t.Fatalf("line %d is %d cells", i, width(l))
		}
		s := ansi.Strip(l)
		if strings.HasSuffix(s, "┃") {
			thumbs++
			if !strings.HasSuffix(strings.TrimSuffix(s, "┃"), " ") {
				t.Fatalf("thumb ate content: %q", s)
			}
		}
	}
	if thumbs == 0 {
		t.Fatal("no thumb drawn")
	}
}

func TestNoColorLegendMatchesBar(t *testing.T) {
	m := fakeModel(true, 140, 40)
	s := m.sessions[sidA]
	b := m.agentBar(m.th.on(m.th.Bg), s, 140)
	bar, legend := ansi.Strip(b[0]), ansi.Strip(b[1])
	rows, _ := agentUsage(s.ByAgent)
	for i := range rows {
		g := plainFills[i%len(plainFills)]
		if !strings.Contains(bar, g) || !strings.Contains(legend, g+" ") {
			t.Fatalf("segment %d: glyph %q must be in the bar and lead its legend entry\nbar    %s\nlegend %s", i, g, bar, legend)
		}
	}
}

func TestHelpAndFooterComeFromKeys(t *testing.T) {
	m := fakeModel(true, 140, 40)
	m.help = true
	out := plain(m)
	for _, g := range helpGroups() {
		for _, b := range g {
			if !strings.Contains(out, b.Help().Key) || !strings.Contains(out, b.Help().Desc) {
				t.Fatalf("help lacks %q %q", b.Help().Key, b.Help().Desc)
			}
		}
	}
	m.help = false
	foot := plain(m)
	foot = foot[strings.LastIndex(foot, "\n"):]
	for _, b := range m.footerKeys() {
		if !strings.Contains(foot, b.Help().Key+" "+b.Help().Desc) {
			t.Fatalf("footer lacks %q: %q", b.Help().Key, foot)
		}
	}
}

func TestNoMouseCapture(t *testing.T) {
	if v := fakeModel(false, 100, 30).View(); v.MouseMode != 0 {
		t.Fatalf("mouse mode %v: the terminal's own selection must keep working", v.MouseMode)
	}
}

// ---- snapshots against the fixture ------------------------------------------------------------

var update = flag.Bool("update", false, "rewrite the golden files")

var goldenNow = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

// fixtureCopy copies tests/fixtures/<name> to a temp dir with a fixed mtime on every file.
func fixtureCopy(t *testing.T, name string) string {
	t.Helper()
	src, dst := "../../tests/fixtures/"+name, t.TempDir()
	mtime := time.Date(2026, 1, 1, 10, 30, 0, 0, time.UTC)
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.WriteFile(out, b, 0o644); err != nil {
			return err
		}
		return os.Chtimes(out, mtime, mtime)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func fixtureSnapshot(t *testing.T, projects, view, session string, w, h int) (string, error) {
	t.Helper()
	root, _ := filepath.Abs("../..")
	r := &data.Runner{Root: root, ProjectsDir: projects, AgentsDir: filepath.Join(root, "tests", "fixtures", "agents")}
	if err := r.Check(); err != nil {
		t.Skipf("python unavailable: %v", err)
	}
	return Snapshot(SnapshotOptions{
		Options: Options{Runner: r, NoColor: true, Range: "all", Now: func() time.Time { return goldenNow }},
		View:    view, Session: session, W: w, H: h,
	})
}

func TestGoldenSnapshots(t *testing.T) {
	for _, fx := range []struct {
		root, prefix string
		views        []string
	}{
		{"projects", "", []string{"dashboard", "session", "agents", "help"}},
		{"grouping", "grouping-", []string{"dashboard"}},
	} {
		projects := fixtureCopy(t, fx.root)
		for _, sz := range [][2]int{{140, 40}, {80, 24}} {
			for _, view := range fx.views {
				name := fmt.Sprintf("%s%s-%dx%d", fx.prefix, view, sz[0], sz[1])
				t.Run(name, func(t *testing.T) {
					got, err := fixtureSnapshot(t, projects, view, "", sz[0], sz[1])
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join("testdata", name+".golden")
					if *update {
						if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
							t.Fatal(err)
						}
						return
					}
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatalf("%v (run go test ./internal/tui -update)", err)
					}
					if got+"\n" != string(want) {
						t.Fatalf("%s changed; run go test ./internal/tui -update and review the diff\n--- got ---\n%s", name, got)
					}
				})
			}
		}
	}
}

func TestSnapshotFailures(t *testing.T) {
	projects := fixtureCopy(t, "projects")
	// the python CLI resolves the prefix: two matches is an error, not a pick
	b, _ := os.ReadFile(filepath.Join(projects, "-fixture-proj", "fixture-0001.jsonl"))
	if err := os.WriteFile(filepath.Join(projects, "-fixture-proj", "fixture-0002.jsonl"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureSnapshot(t, projects, "session", "fixture", 100, 30); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix: %v", err)
	}
	if _, err := fixtureSnapshot(t, projects, "session", "nope", 100, 30); err == nil {
		t.Fatal("unknown session must fail")
	}
	if out, err := fixtureSnapshot(t, projects, "session", "fixture-0001", 100, 30); err != nil || !strings.Contains(out, "fixture-0001") {
		t.Fatalf("exact id: %v", err)
	}
	// a load that fails exits non-zero: no python, or a missing projects dir
	missing := &data.Runner{Root: "../..", Python: "definitely-not-python-xyz"}
	if _, err := Snapshot(SnapshotOptions{Options: Options{Runner: missing}, View: "dashboard", W: 100, H: 30}); err == nil {
		t.Fatal("missing python must fail the snapshot")
	}
	bad := &data.Runner{Root: "../..", ProjectsDir: filepath.Join(projects, "does-not-exist")}
	if _, err := Snapshot(SnapshotOptions{Options: Options{Runner: bad}, View: "dashboard", W: 100, H: 30}); err == nil {
		t.Fatal("an unreadable projects dir must fail the snapshot")
	}
}

// burstSession is a six-day-style session: two bursts of equal activity 66 hours apart,
// with a one-second dispatch in the second.
func burstSession(m *model) (*data.Session, *data.Session) {
	s := m.sessions[sidA]
	d := &data.Session{ID: sidA, Dispatches: []data.Dispatch{
		dispatchAt("a1", "explorer", 0, 5),
		dispatchAt("a2", "coder", 5, 5),
		dispatchAt("b1", "coder", 10+66*60, 5),
		dispatchAt("b2", "reviewer", 10+66*60+5, 4.99),
		dispatchAt("b3", "reviewer", 10+66*60+9.99, 1.0/60),
	}}
	m.details[sidA] = d
	m.view, m.selID = viewSession, sidA
	return s, d
}

func TestTimelineSkipsIdleTime(t *testing.T) {
	m := fakeModel(true, 140, 40)
	s, d := burstSession(m)
	pl := m.timelinePlan(s, d, 140, 8, 0)
	var bursts, breaks []axisSeg
	for _, sg := range pl.axis.segs {
		if sg.brk {
			breaks = append(breaks, sg)
		} else {
			bursts = append(bursts, sg)
		}
	}
	if len(bursts) != 2 || len(breaks) != 1 || breaks[0].marker() != "┆ 2d 18h ┆" {
		t.Fatalf("want two bursts around one break labelled 2d 18h: %+v", pl.axis.segs)
	}
	if a, b := bursts[0].w, bursts[1].w; a < 40 || b < 40 || a+b+breaks[0].w != pl.axis.tw {
		t.Fatalf("bursts of equal activity share the width: %d + %d + break %d of %d", a, b, breaks[0].w, pl.axis.tw)
	}
	last := d.Dispatches[4]
	st, en, _ := dispatchBounds(last, pl.clock)
	if pl.axis.x(st) < bursts[1].x || pl.axis.x(en) >= bursts[1].x+bursts[1].w {
		t.Fatal("a one-second bar stays inside its burst")
	}
	out := plain(m)
	for _, want := range []string{"┆ 2d 18h ┆", "11-13 10:00", "11-16 04:10", "11-16 04:20"} {
		if !strings.Contains(out, want) {
			t.Fatalf("timeline lacks %q:\n%s", want, out)
		}
	}
}

func TestTimelineKeepsTheSelectedLane(t *testing.T) {
	m := fakeModel(true, 140, 40)
	s := m.sessions[sidA]
	d := &data.Session{ID: sidA}
	for i, a := range []string{"main", "coder", "reviewer", "explorer", "Plan", "zed"} {
		d.Dispatches = append(d.Dispatches, dispatchAt(fmt.Sprint(i), a, float64(i), 1))
	}
	pl := m.timelinePlan(s, d, 140, 3, 5) // room for two rows and the "more" line
	if len(pl.lanes) != 2 || pl.hidden != 4 || pl.lanes[1].agent != "zed" || !pl.lanes[1].first || pl.lanes[1].items[0] != 5 {
		t.Fatalf("the lane holding the selection must stay: %+v", pl)
	}
	if pl = m.timelinePlan(s, d, 140, 3, 0); pl.lanes[1].agent != "coder" {
		t.Fatalf("without a hidden selection the first rows stay: %+v", pl.lanes)
	}
}

func TestAgentOrderIsFixed(t *testing.T) {
	want := []string{"main", "coder", "factory:coder", "zz:coder", "reviewer", "factory:reviewer", "explorer", "general-purpose", "Explore", "Plan", "fork", "alpha", "vercel:beta", "zed"}
	got := []string{"zz:coder", "zed", "factory:reviewer", "vercel:beta", "fork", "factory:coder", "Plan", "explorer", "alpha", "main", "Explore", "reviewer", "general-purpose", "coder"}
	sort.Slice(got, func(i, j int) bool { return agentLess(got[i], got[j]) })
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v", got)
	}
	// by usage it would be the other way round: the order must not follow it
	rows, _ := agentUsage(map[string]data.Usage{"explorer": usage(0, 0, 0, 900), "coder": usage(0, 0, 0, 1), "main": usage(0, 0, 0, 50)})
	if rows[0].k != "main" || rows[1].k != "coder" || rows[2].k != "explorer" {
		t.Fatalf("usage rows %v", rows)
	}
	m := fakeModel(true, 140, 40)
	m.view = viewAgents
	out := plain(m)
	if !(strings.Index(out, "main") < strings.Index(out, "coder") && strings.Index(out, "coder") < strings.Index(out, "explorer") && strings.Index(out, "explorer") < strings.Index(out, "odd-one")) {
		t.Fatalf("agents view not in the fixed order:\n%s", out)
	}
}

func TestAgentsViewSaysWhenMoreExist(t *testing.T) {
	m := fakeModel(true, 80, 24)
	for i := 0; i < 4; i++ {
		m.agents.Agents = append(m.agents.Agents, data.AgentRow{Agent: fmt.Sprintf("x%d", i), Usage: usage(1, 1, 1, 1)})
	}
	m.view = viewAgents
	if out := plain(m); !strings.Contains(out, "+4 more agent types") {
		t.Fatalf("a cut-off agents list says so:\n%s", out)
	}
}

func TestListShortensFlagsDetailSpellsThemOut(t *testing.T) {
	if got := shortFlag("review-loop(resume x1)"); got != "loop↻1" {
		t.Fatalf("shortFlag = %q", got)
	}
	m := fakeModel(true, 140, 40)
	m.details[sidA].Dispatches[0].Flags = []string{"review-loop(resume x1)"}
	m.view, m.selID = viewSession, sidA
	out := plain(m)
	if !strings.Contains(out, "[loop↻1]") || !strings.Contains(out, "[review-loop(resume x1)]") {
		t.Fatalf("list abbreviates, detail pane spells out:\n%s", out)
	}
}

func TestDetailStatusLineKeepsTheDuration(t *testing.T) {
	m := fakeModel(true, 140, 40)
	d := m.details[sidA]
	d.Dispatches[0].AgentType = "general-purpose"
	m.view, m.selID = viewSession, sidA
	if out := plain(m); !strings.Contains(out, "● general-purpose · completed") || !strings.Contains(out, "→ 10:04:00 · 3m00s") {
		t.Fatalf("detail status or span line cut:\n%s", out)
	}
}

func TestUnknownStatusIsNotGreen(t *testing.T) {
	m := fakeModel(false, 140, 40)
	m.details[sidA].Dispatches[0].Status = "killed"
	m.view, m.selID = viewSession, sidA
	out := m.Render()
	i := strings.Index(out, "killed")
	if i < 0 {
		t.Fatal("status not shown")
	}
	sgr := out[strings.LastIndex(out[:i], "\x1b["):i]
	if strings.Contains(sgr, "88;214;141") {
		t.Fatalf("an unknown status must not look like success: %q", sgr)
	}
}

func TestBreakMarkersLineUpOnLaneAndAxis(t *testing.T) {
	for _, gap := range []float64{5, 14, 48, 50, 12*60 + 5, 66 * 60} { // even and odd marker widths
		m := fakeModel(true, 140, 40)
		d := &data.Session{ID: sidA, Dispatches: []data.Dispatch{
			dispatchAt("a", "coder", 0, 5), dispatchAt("b", "coder", 5+gap, 5),
		}}
		m.details[sidA] = d
		m.view, m.selID = viewSession, sidA
		var cols [][]int
		for _, line := range strings.Split(plain(m), "\n") {
			var c []int
			for i, r := range []rune(line) {
				if r == '┆' {
					c = append(c, i)
				}
			}
			if len(c) > 0 {
				cols = append(cols, c)
			}
		}
		if len(cols) < 2 {
			t.Fatalf("gap %vm: want the lane and the axis to show the break:\n%s", gap, plain(m))
		}
		for _, c := range cols[1:] {
			if fmt.Sprint(c) != fmt.Sprint(cols[0]) {
				t.Fatalf("gap %vm: ┆ columns differ between rows: %v", gap, cols)
			}
		}
	}
}

func TestTimeAxisLimits(t *testing.T) {
	for _, tw := range []int{1, 4, 12, 24, 40, 60, 80, 120, 200} {
		// ten bursts split by long gaps (more breaks than fit), the first one tiny and
		// the last one long
		var ivs []interval
		at := t0
		for i := 0; i < 10; i++ {
			dur := time.Minute
			if i == 9 {
				dur = 8 * time.Hour
			}
			ivs = append(ivs, interval{at, at.Add(dur)})
			at = at.Add(dur + 3*time.Hour)
		}
		a := newTimeAxis(ivs, tw)
		bursts, sum := 0, 0
		for i, sg := range a.segs {
			if sg.x != sum {
				t.Fatalf("tw %d: segment %d starts at %d, want %d", tw, i, sg.x, sum)
			}
			sum += sg.w
			if sg.brk {
				continue
			}
			bursts++
			if sg.w < 1 || (tw >= 10*minBurstW+len(a.segs)*breakW && sg.w < minBurstW) {
				t.Fatalf("tw %d: burst %d is %d wide", tw, i, sg.w)
			}
		}
		if sum != max(tw, bursts) {
			t.Fatalf("tw %d: widths sum to %d", tw, sum)
		}
		for _, iv := range ivs {
			for _, x := range []int{a.x(iv.start), a.x(iv.end)} {
				in := false
				for _, sg := range a.segs {
					in = in || (!sg.brk && x >= sg.x && x < sg.x+sg.w)
				}
				if x < 0 || x >= sum || !in {
					t.Fatalf("tw %d: a bar end at column %d is outside the bursts of %+v", tw, x, a.segs)
				}
			}
		}
	}
}

func TestFirstBurstGetsLabelRoom(t *testing.T) {
	ivs := []interval{{t0, t0.Add(15 * time.Minute)}, {t0.Add(10 * time.Hour), t0.Add(20 * time.Hour)}}
	if a := newTimeAxis(ivs, 80); a.segs[0].w < minBurstW {
		t.Fatalf("a short first burst keeps room for a label: %+v", a.segs)
	}
}
