package tui

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"isshoni/internal/data"
)

// ---- keys and pages ---------------------------------------------------------------

func TestKaizenViewKeys(t *testing.T) {
	m := fakeModel(true, 120, 40)
	send(m, "5")
	if m.view != viewKaizen {
		t.Fatal("5 opens the kaizen view")
	}
	if !strings.Contains(plain(m), "[5 Kaizen]") {
		t.Fatal("the tab is in the header")
	}
	send(m, "tab")
	if m.view != viewDashboard {
		t.Fatalf("tab wraps from kaizen to the dashboard: %v", m.view)
	}
	send(m, "shift+tab")
	if m.view != viewKaizen {
		t.Fatalf("shift+tab goes back to kaizen: %v", m.view)
	}
	send(m, "esc")
	if m.view != viewDashboard {
		t.Fatal("esc goes back to the dashboard")
	}
}

func TestKaizenPagesAndScroll(t *testing.T) {
	m := kaizenModel(t, true, 80, 24, pagePulse)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.kz.page != pageFlow {
		t.Fatalf("→ -> %v", m.kz.page)
	}
	send(m, "l")
	if m.kz.page != pageBoard {
		t.Fatalf("l -> %v", m.kz.page)
	}
	send(m, "l")
	if m.kz.page != pagePulse {
		t.Fatalf("pages wrap: %v", m.kz.page)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	send(m, "h")
	if m.kz.page != pageFlow {
		t.Fatalf("← and h go back: %v", m.kz.page)
	}
	send(m, "j", "j", "j")
	if m.kz.top[pageFlow] != 3 || m.kz.top[pagePulse] != 0 {
		t.Fatalf("scroll is per page: %v", m.kz.top)
	}
	send(m, "l", "j")
	send(m, "h")
	if m.kz.top[pageFlow] != 3 || m.kz.top[pageBoard] != 1 {
		t.Fatalf("each page keeps its place: %v", m.kz.top)
	}
	send(m, "G")
	if want := m.kaizenPositions() - 1; m.kz.top[pageFlow] != want {
		t.Fatalf("G goes to the end of the page: %d want %d", m.kz.top[pageFlow], want)
	}
	if out := plain(m); strings.Contains(out, "more lines") {
		t.Fatalf("the end shows no more lines:\n%s", out)
	}
	dash := fakeModel(true, 100, 30)
	send(dash, "l", "h")
	if dash.kz.page != pagePulse {
		t.Fatal("←→ turn the page only in the kaizen view")
	}
	for _, name := range []string{"Pulse", "Flow", "Board"} {
		if !strings.Contains(plain(m), name) {
			t.Fatalf("the sub-header names the page %s", name)
		}
	}
}

// ---- loading ----------------------------------------------------------------------

func kaizenStub(t *testing.T) (*model, string, *time.Time) {
	t.Helper()
	m, dir := stubModel(t)
	b, err := os.ReadFile("testdata/kaizen-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kaizen.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	return m, dir, &now
}

func TestKaizenLoadsOnEntryNotWithinThirtySeconds(t *testing.T) {
	m, dir, now := kaizenStub(t)
	got := only[kaizenMsg](upd(m, press("5")))
	if len(got) != 1 || calls(t, dir, "kaizen --since") != 1 || !m.kz.busy {
		t.Fatalf("entering starts one load for the range: %d msgs, %d calls", len(got), calls(t, dir, "kaizen "))
	}
	if more := only[kaizenMsg](upd(m, press("5"))); len(more) != 0 {
		t.Fatal("one load in flight at most")
	}
	if out := plain(m); !strings.Contains(out, "reading the factory's logs") {
		t.Fatalf("the first load shows a spinner line:\n%s", out)
	}
	upd(m, got[0])
	if m.kz.busy || m.kz.doc == nil || len(m.kz.doc.Tasks) != 24 || m.kz.doc.Summary.Landed != 20 {
		t.Fatalf("the result is applied: %+v", m.kz.busy)
	}
	send(m, "1")
	*now = now.Add(29 * time.Second)
	if got := only[kaizenMsg](upd(m, press("5"))); len(got) != 0 {
		t.Fatal("a result under 30 s old is kept")
	}
	send(m, "1")
	*now = now.Add(2 * time.Second)
	if got := only[kaizenMsg](upd(m, press("5"))); len(got) != 1 {
		t.Fatal("an older one is loaded again")
	}
	if n := calls(t, dir, "kaizen "); n != 2 {
		t.Fatalf("%d calls", n)
	}
}

func TestKaizenRangeChangeLoadsAgainWithTheNewSince(t *testing.T) {
	m, dir, _ := kaizenStub(t)
	upd(m, only[kaizenMsg](upd(m, press("5")))[0])
	msgs := upd(m, press("r")) // 7d -> 30d
	if got := only[kaizenMsg](msgs); len(got) != 1 {
		t.Fatalf("a range change loads: %d", len(got))
	}
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(b), "kaizen --since\n") || calls(t, dir, "kaizen ") != 2 {
		t.Fatalf("calls:\n%s", b)
	}
	// the range "all" passes no --since
	only[kaizenMsg](upd(m, press("r")))
	b, _ = os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.Contains(string(b), "kaizen --json\n") {
		t.Fatalf("all passes no --since:\n%s", b)
	}
}

func TestKaizenStaleResultIsIgnored(t *testing.T) {
	m, _, _ := kaizenStub(t)
	first := only[kaizenMsg](upd(m, press("5")))[0]
	upd(m, press("r")) // a newer load replaces it
	upd(m, first)
	if m.kz.doc != nil || !m.kz.busy {
		t.Fatal("a superseded result is dropped")
	}
}

func TestKaizenFailedLoadKeepsTheOldResult(t *testing.T) {
	m, dir, now := kaizenStub(t)
	upd(m, only[kaizenMsg](upd(m, press("5")))[0])
	send(m, "1")
	*now = now.Add(time.Minute)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	got := only[kaizenMsg](upd(m, press("5")))
	if len(got) != 1 {
		t.Fatal("no load")
	}
	upd(m, got[0])
	out := plain(m)
	if m.kz.doc == nil || m.kz.err != "boom" || !strings.Contains(out, "⚠ boom") || !strings.Contains(out, "LANDED") {
		t.Fatalf("one red line with the first line of the error above the old result:\n%s", out)
	}
	if strings.Count(out, "⚠") != 1 {
		t.Fatal("one line")
	}
	os.Remove(filepath.Join(dir, "fail"))
	upd(m, only[kaizenMsg](upd(m, press("5")))[0])
	if m.kz.err != "" {
		t.Fatal("a good load clears the error")
	}
	// the very first load failing shows the error alone
	fresh := fakeModel(true, 80, 24)
	fresh.view = viewKaizen
	fresh.applyKaizen(kaizenMsg{gen: 0, err: errors.New("first\nsecond")})
	if out := plain(fresh); !strings.Contains(out, "⚠ first") || strings.Contains(out, "second") {
		t.Fatalf("first line of the error:\n%s", out)
	}
}

// ---- frames -----------------------------------------------------------------------

func TestKaizenFramesFitEverySize(t *testing.T) {
	docs := map[string]*data.Kaizen{"full": kaizenDoc(t), "few": fewDoc(t), "empty": {}}
	gh := kaizenDoc(t)
	gh.Repos[0].PRs = "gh not found on PATH"
	docs["no gh"] = gh
	long := kaizenDoc(t)
	long.Tasks[0].Slug = strings.Repeat("長い", 30)
	long.Stops[0].Why = strings.Repeat("why ", 60)
	long.Outbox.Entries[0].Text = strings.Repeat("a-very-long-word-without-spaces", 8)
	docs["long"] = long
	for name, doc := range docs {
		for _, sz := range sizes {
			for _, nc := range []bool{false, true} {
				for page := pagePulse; page <= pageBoard; page++ {
					m := fakeModel(nc, sz[0], sz[1])
					setKaizen(m, doc, page)
					checkFrame(t, fmt.Sprintf("kaizen %s page %d %dx%d nc=%v", name, page, sz[0], sz[1], nc), m, nc)
					m.kz.top[page] = 1 << 20
					m.fixScroll()
					checkFrame(t, fmt.Sprintf("kaizen %s page %d %dx%d nc=%v scrolled", name, page, sz[0], sz[1], nc), m, nc)
				}
			}
		}
	}
	for _, nc := range []bool{false, true} {
		m := fakeModel(nc, 80, 24)
		m.view = viewKaizen
		checkFrame(t, "kaizen loading", m, nc)
		m.kz.err = "boom"
		checkFrame(t, "kaizen first load failed", m, nc)
	}
}

func TestKaizenEmptyMessageOnEveryPage(t *testing.T) {
	for page := pagePulse; page <= pageBoard; page++ {
		m := fakeModel(true, 100, 30)
		setKaizen(m, &data.Kaizen{StoreFound: true}, page)
		if out := plain(m); !strings.Contains(out, "No factory tasks yet") || !strings.Contains(out, "/factory:dispatch") {
			t.Fatalf("page %d:\n%s", page, out)
		}
	}
}

func TestKaizenNullsShowDashes(t *testing.T) {
	doc := kaizenDoc(t)
	for i := range doc.Tasks {
		doc.Tasks[i].Cost, doc.Tasks[i].Rounds, doc.Tasks[i].BriefToPR, doc.Tasks[i].PRToMerge, doc.Tasks[i].Version = nil, nil, nil, nil, ""
	}
	doc.Summary.Cost.Median, doc.Summary.BriefToPR.Median, doc.Summary.PRToMerge.Median, doc.Summary.ReworkShare, doc.Summary.Trend = nil, nil, nil, nil, nil
	m := fakeModel(true, 140, 40)
	setKaizen(m, doc, pageFlow)
	out := plain(m)
	if !strings.Contains(out, "v?") || !strings.Contains(out, "fx-login-flow") {
		t.Fatalf("a task without values still gets a row of dashes:\n%s", out)
	}
	m.kz.page = pagePulse
	out = plain(m)
	if !strings.Contains(out, "0 landed tasks: charts start at 2") {
		t.Fatalf("no chart without values:\n%s", out)
	}
}

// ---- sections ---------------------------------------------------------------------

func TestKaizenPulseSections(t *testing.T) {
	out := plain(kaizenModel(t, true, 140, 40, pagePulse))
	for _, want := range []string{"LANDED", "3 in flight", "COST / TASK", "$6.20", "↓ 44% vs earlier 10", "BRIEF → PR", "25m50s", "PR → MERGE", "5m45s", "FIRST PASS", "7/19", "rework 9% of cost",
		"✦ 4 landed first-pass in a row · fx-quiet-refactor", "✦ record low cost: $2.31", "cost per task", "brief → pull request", "review rounds", "pull request → merge",
		"v0.3.0", "defects caught by", "tests 5", "reviewer 39", "human 1", "14 open issues · 7 decisions · 7 acted on  → Board", "★"} {
		if !strings.Contains(out, want) {
			t.Errorf("pulse lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "none escaped") || strings.Contains(out, "unavailable") {
		t.Errorf("a human escape and working gh:\n%s", out)
	}

	doc := kaizenDoc(t)
	doc.Summary.Defects.Human = 0
	doc.Rewards = nil
	doc.Summary.Trend = nil
	doc.Repos[1].PRs = "gh failed: not a GitHub repository"
	m := fakeModel(true, 140, 50)
	setKaizen(m, doc, pagePulse)
	out = plain(m)
	for _, want := range []string{"none escaped to CI or the human", "comparing latest with earlier starts at 10 landed tasks (have 20)", "3 in flight", "20 tasks",
		"pull requests unavailable for fixture-beta: gh failed: not a GitHub repository; landed counts and times need gh"} {
		if !strings.Contains(out, want) {
			t.Errorf("variant lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "★") {
		t.Error("no reward, no star")
	}
	for i := range doc.Tasks {
		doc.Tasks[i].Defects = nil
	}
	setKaizen(m, doc, pagePulse)
	if out := plain(m); !strings.Contains(out, "no retrospectives yet") {
		t.Errorf("no retros:\n%s", out)
	}
}

func TestKaizenSiteManagerRecord(t *testing.T) {
	pulse := func(s data.SiteManager, w int) string {
		doc := kaizenDoc(t)
		doc.SiteManager = s
		m := fakeModel(true, w, 200)
		setKaizen(m, doc, pagePulse)
		out := plain(m)
		return out[strings.Index(out, "site manager"):strings.Index(out, "open issues")]
	}
	has := func(out string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(out, want) {
				t.Errorf("lacks %q:\n%s", want, out)
			}
		}
	}
	full := kaizenDoc(t).SiteManager
	has(pulse(full, 140), "precision 75%  3 acted on of 4 with an outcome", "recall 60%  3 acted on of 5 that needed the human",
		"6 escalated   3 acted on   1 ignored   2 no outcome yet   4 suppressed   2 missed (1 suppressed first, 1 never fired)")
	has(pulse(full, 60), "precision 75%", "recall 60%", "2 no outcome yet", "2 missed (1 suppressed first, 1 never fired)")

	// the log is empty until the site manager first fires: say so, and how outcomes get recorded
	empty := pulse(data.SiteManager{}, 140)
	has(empty, "nothing escalated, suppressed or missed yet", "precision and recall start with the first outcome (factory firing <id> acted|ignored)")
	if strings.Contains(empty, "precision 0%") || strings.Contains(empty, "0 escalated") {
		t.Errorf("an empty record shows no numbers:\n%s", empty)
	}

	// escalations without an outcome give no ratio, and a zero is a number, not a dash
	has(pulse(data.SiteManager{Escalated: 2, NoOutcome: 2, Suppressed: 1}, 140),
		"precision -  no outcome recorded yet", "recall -  nothing acted on or missed yet", "2 escalated   0 acted on   0 ignored   2 no outcome yet   1 suppressed   0 missed")
	has(pulse(data.SiteManager{Escalated: 1, Ignored: 1, Missed: 1, MissedNeverFired: 1, Precision: ptr(0.0), Recall: ptr(0.0)}, 140),
		"precision 0%  0 acted on of 1 with an outcome", "recall 0%  0 acted on of 1 that needed the human", "1 missed (0 suppressed first, 1 never fired)")
}

func TestKaizenStopReasonIsShownInFull(t *testing.T) {
	why := "Should the hook payload keep the legacy session field? Two consumers still read it, and dropping it would break the replay tool until both are migrated."
	for _, w := range []int{140, 100, 80, 60} {
		m := kaizenModel(t, true, w, 200, pageFlow)
		out := plain(m)
		out = out[strings.Index(out, "the line stopped"):strings.Index(out, "what slowed tasks")]
		if got := strings.Join(strings.Fields(out), " "); !strings.Contains(got, why) || !strings.Contains(got, "git push was denied: the remote rejects the task branch") {
			t.Errorf("%d columns: the reasons are not whole:\n%s", w, out)
		}
		if !strings.Contains(out, "reason not recorded") {
			t.Errorf("%d columns: a stop without a summary says so:\n%s", w, out)
		}
	}
}

func TestKaizenFlowSections(t *testing.T) {
	out := plain(kaizenModel(t, true, 140, 40, pageFlow))
	for _, want := range []string{"where the money went", "lead $38.53 31%", "rework 9% (subagent runs after the first review)", "where the time went", "building 8h19m 69%", "waiting for merge 2h16m 19%",
		"fixture-alpha  18 tasks", "SLUG", "ROLES", "✓ landed", "▲ blocked", "◆ handed-over", "· dismissed", "● working"} {
		if !strings.Contains(out, want) {
			t.Errorf("flow lacks %q:\n%s", want, out)
		}
	}
	m := kaizenModel(t, true, 140, 40, pageFlow)
	m.kz.top[pageFlow] = 1 << 20
	m.fixScroll()
	out = plain(m)
	for _, want := range []string{"stops", "QUESTION", "BLOCKED", "OVERTIME", "DENIED", "waiting", "Bash: git push --force is not allowed in auto mode", "reason not recorded", "what slowed tasks", "fx-quiet-refactor"} {
		if !strings.Contains(out, want) {
			t.Errorf("flow end lacks %q:\n%s", want, out)
		}
	}
	// newest first
	stops := out[strings.Index(out, "the line stopped"):]
	if strings.Index(stops, "fx-websocket-hooks") > strings.Index(stops, "fx-login-flow") {
		t.Errorf("stops are newest first:\n%s", out)
	}
	doc := kaizenDoc(t)
	doc.Stops = nil
	m = fakeModel(true, 140, 40)
	setKaizen(m, doc, pageFlow)
	m.kz.top[pageFlow] = 1 << 20
	m.fixScroll()
	if out := plain(m); !strings.Contains(out, "nothing stopped the line") {
		t.Errorf("no stops:\n%s", out)
	}
	// narrow terminals drop ROLES, VER, REWORK and MERGE in that order
	for _, c := range []struct {
		w          int
		gone, kept []string
	}{
		{140, nil, []string{"ROLES", "VER", "REWORK", "MERGE"}},
		{80, []string{"ROLES", "VER"}, []string{"REWORK", "MERGE", "STOPS"}},
		{60, []string{"ROLES", "VER", "REWORK"}, []string{"BUILD", "COST", "STOPS"}},
	} {
		out := plain(kaizenModel(t, true, c.w, 40, pageFlow))
		for _, g := range c.gone {
			if strings.Contains(out, " "+g+" ") {
				t.Errorf("%d columns should drop %s:\n%s", c.w, g, out)
			}
		}
		for _, k := range c.kept {
			if !strings.Contains(out, k) {
				t.Errorf("%d columns should keep %s:\n%s", c.w, k, out)
			}
		}
	}
}

func TestKaizenBoardSections(t *testing.T) {
	m := kaizenModel(t, true, 140, 60, pageBoard)
	out := plain(m)
	for _, want := range []string{"14 open · 2 accepted · 2 done · 2 rejected", "6 open · 1 accepted", "open issues by topic", "grouped by keyword", "brief  ", "permissions", "+2 more",
		"acted on", "[accepted]", "[done]", "[rejected]", "#125", "decisions", "kept the old endpoint for one release"} {
		if !strings.Contains(out, want) {
			t.Errorf("board lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DECISION:") {
		t.Errorf("the prefix is dropped:\n%s", out)
	}
	if strings.Index(out, "brief ") > strings.Index(out, "other ") {
		t.Errorf("largest topic first, other last:\n%s", out)
	}
	doc := kaizenDoc(t)
	for i := range doc.Outbox.Entries {
		if doc.Outbox.Entries[i].Status != "open" {
			doc.Outbox.Entries[i].Status = "open"
		}
	}
	setKaizen(m, doc, pageBoard)
	if out := plain(m); !strings.Contains(out, "nothing acted on yet: `factory feedback set <id> accepted|done|rejected`") {
		t.Errorf("nothing acted on:\n%s", out)
	}
}

// ---- chart ------------------------------------------------------------------------

func chartText(t *testing.T, vals []float64, med float64, w int) (chartOut, []string) {
	t.Helper()
	p := NewTheme(true).on(nil)
	out := p.chart(chartOpts{vals: vals, median: med, hasMedian: true, grow: 1,
		label: func(x float64) string { return fmt.Sprintf("%.0f", x) },
		pick:  func(int) color.Color { return p.th.Blue }}, w)
	var lines []string
	for _, l := range out.lines {
		if width(l) != w {
			t.Fatalf("chart line is %d cells, want %d: %q", width(l), w, l)
		}
		lines = append(lines, ansi.Strip(l))
	}
	return out, lines
}

func TestChartColumnsMedianAndHeights(t *testing.T) {
	vals := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	out, lines := chartText(t, vals, 5.5, 40)
	if out.first != 0 || out.stride != 3 || out.gutter != 3 {
		t.Fatalf("ten columns of two cells with a gap fit 37 cells: %+v", out)
	}
	n := 0
	for _, r := range []rune(lines[4])[out.gutter:] {
		if r != ' ' {
			n++
		}
	}
	if n != 20 {
		t.Fatalf("bottom row has ten two-cell columns, got %d blocks:\n%s", n, strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(lines[0], "10 ") || strings.Count(lines[0], "█") != 2 {
		t.Fatalf("the top row labels the max and only the tallest column reaches it: %q", lines[0])
	}
	// the median 5.5 of 10 is level 22: row 2, labelled, dotted over the empty cells and under the tall columns
	if !strings.HasPrefix(strings.TrimLeft(lines[2], " "), "6 ·") {
		t.Fatalf("median rule on its row: %q", lines[2])
	}
	if r := []rune(lines[2]); string(r[out.gutter+9*out.stride]) != "█" {
		t.Fatalf("the tallest column covers the rule: %q", lines[2])
	}
	if strings.Contains(lines[4], "·") || strings.Contains(lines[3], "·") {
		t.Fatalf("no rule on other rows: %q", lines[3:])
	}
	// eighth blocks: half the tallest is four rows of the pair's second column and a half block
	_, lines = chartText(t, []float64{8, 4}, 6, 10)
	if !strings.Contains(lines[2], "▄") || !strings.Contains(lines[0], "██") {
		t.Fatalf("heights come from eighth blocks:\n%s", strings.Join(lines, "\n"))
	}
}

func TestChartKeepsTheNewestThatFit(t *testing.T) {
	var vals []float64
	for i := 0; i < 50; i++ {
		vals = append(vals, float64(i+1))
	}
	out, lines := chartText(t, vals, 25, 30)
	shown := (30 - out.gutter + 1) / 2
	if out.stride != 2 || out.first != 50-shown || strings.Count(lines[4], "█") != shown {
		t.Fatalf("one cell per column and a gap, the newest %d of 50: %+v", shown, out)
	}
	for x, want := range map[float64]string{1: "1", 1.5: "1.5", 3: "3", 3.5: "3.5"} {
		if got := kzRounds(x); got != want {
			t.Fatalf("rounds %v shown as %q", x, got)
		}
	}
	m := fakeModel(true, 100, 30)
	setKaizen(m, kaizenDoc(t), pagePulse)
	s := m.kaizenSeries()[0]
	for len(s.vals) < 60 {
		s.vals, s.slugs, s.vers = append(s.vals, 3), append(s.slugs, "x"), append(s.vers, "0.3.0")
	}
	box := ansi.Strip(strings.Join(m.chartBox(m.th.on(m.th.Bg), s, 40), "\n"))
	if !strings.Contains(box, "/60") || !strings.Contains(box, "last ") {
		t.Fatalf("the title says how many are shown:\n%s", box)
	}
}

func TestChartVersionMarkers(t *testing.T) {
	m := fakeModel(true, 100, 30)
	setKaizen(m, kaizenDoc(t), pagePulse)
	p := m.th.on(nil)
	vers := []string{"0.1.0", "0.1.0", "0.1.0", "0.2.0", "0.2.0", "0.2.0", "0.3.0", "0.3.0", "0.3.0"}
	out, _ := chartText(t, []float64{1, 2, 3, 4, 5, 6, 7, 8, 9}, 3, 40)
	axis, labels := m.versionAxis(p, vers, out, 40)
	a, l := ansi.Strip(axis), ansi.Strip(labels)
	if x := len([]rune(a[:strings.Index(a, "▲")])); x != out.gutter+3*out.stride {
		t.Fatalf("▲ under the first task of v0.2.0: at %d in %q", x, a)
	}
	if strings.Count(a, "▲") != 2 || !strings.HasPrefix(l, strings.Repeat(" ", out.gutter)+"v0.1.0") || !strings.Contains(l, "v0.2.0") || !strings.Contains(l, "v0.3.0") {
		t.Fatalf("one marker per later version, the first labelled at the left:\n%q\n%q", a, l)
	}
	if width(axis) != 40 {
		t.Fatalf("axis is %d wide", width(axis))
	}
}

func TestChartBoxNeedsTwoColumns(t *testing.T) {
	m := fakeModel(true, 100, 30)
	setKaizen(m, kaizenDoc(t), pagePulse)
	s := m.kaizenSeries()[0]
	for n, want := range map[int]string{0: "0 landed tasks: charts start at 2", 1: "1 landed task: charts start at 2"} {
		s.vals, s.slugs, s.vers = s.vals[:n], s.slugs[:n], s.vers[:n]
		box := ansi.Strip(strings.Join(m.chartBox(m.th.on(m.th.Bg), s, 50), "\n"))
		if !strings.Contains(box, want) || strings.Contains(box, "█") {
			t.Fatalf("%d columns:\n%s", n, box)
		}
		if n == 1 && !strings.Contains(box, "the one value") {
			t.Fatal("the one value is shown")
		}
	}
}

func TestChartLayoutAcrossWidths(t *testing.T) {
	for w, rows := range map[int]int{200: 1, 147: 1, 140: 2, 73: 2, 72: 4, 60: 4} {
		m := fakeModel(true, w, 40)
		setKaizen(m, kaizenDoc(t), pagePulse)
		out := ansi.Strip(strings.Join(m.pulseCharts(m.th.on(m.th.Bg), w), "\n"))
		if got := strings.Count(out, "╭"); got != 4 {
			t.Fatalf("%d: %d boxes", w, got)
		}
		if got := len(strings.Split(out, "\n")) / chartH; got != rows {
			t.Errorf("%d columns: %d rows of charts, want %d", w, got, rows)
		}
	}
}

// ---- trend ------------------------------------------------------------------------

func TestKaizenTrendBand(t *testing.T) {
	m := fakeModel(false, 140, 40)
	for _, c := range []struct {
		latest, earlier float64
		arrow           string
		pct             int
		col             func(*Theme) color.Color
	}{
		{4.4, 5, "↓", 12, func(th *Theme) color.Color { return th.Green }},
		{4.5, 5, "≈", 10, func(th *Theme) color.Color { return th.Muted }},
		{5, 5, "≈", 0, func(th *Theme) color.Color { return th.Muted }},
		{5.5, 5, "≈", 10, func(th *Theme) color.Color { return th.Muted }},
		{5.6, 5, "↑", 12, func(th *Theme) color.Color { return th.Orange }},
		{1, 0, "↑", 100, func(th *Theme) color.Color { return th.Orange }},
		{0, 0, "≈", 0, func(th *Theme) color.Color { return th.Muted }},
	} {
		arrow, pct, col := m.trendLook(c.latest, c.earlier)
		if arrow != c.arrow || pct != c.pct || key24(col) != key24(c.col(m.th)) {
			t.Errorf("%v vs %v: %s %d%%", c.latest, c.earlier, arrow, pct)
		}
	}
	// painted in the tile: lower is green, higher is orange
	doc := kaizenDoc(t)
	doc.Summary.Trend.Cost = data.Pair{Latest: ptr(6.0), Earlier: ptr(5.0)}
	setKaizen(m, doc, pagePulse)
	sp := m.th.on(m.th.Surface)
	if out := m.Render(); !strings.Contains(out, sp.t("↑ 20% vs earlier 10", m.th.Orange)) || !strings.Contains(out, sp.t("↓ 61% vs earlier 10", m.th.Green)) {
		t.Errorf("trend colours:\n%s", ansi.Strip(out))
	}
	doc.Summary.Trend.Cost = data.Pair{Latest: ptr(5.2), Earlier: ptr(5.0)}
	setKaizen(m, doc, pagePulse)
	if out := m.Render(); !strings.Contains(out, sp.t("≈ 4% vs earlier 10", m.th.Muted)) {
		t.Errorf("a change within 10%% is muted:\n%s", ansi.Strip(out))
	}
}

// ---- motion -----------------------------------------------------------------------

func TestKaizenTickerStopsOnceSettled(t *testing.T) {
	now := t0
	m := New(Options{Now: func() time.Time { return now }})
	m.loading = false
	setKaizen(m, kaizenDoc(t), pagePulse)
	m.kz.shownAt, m.kz.born = now, now
	if !m.animating() {
		t.Fatal("columns grow and the rewards shimmer after a load")
	}
	if g := m.kaizenGrow(); g != 0 {
		t.Fatalf("columns start at zero: %v", g)
	}
	m.kaizenRetarget(false)
	now = now.Add(400 * time.Millisecond)
	if g := m.kaizenGrow(); g <= 0 || g >= 1 {
		t.Fatalf("mid growth: %v", g)
	}
	now = now.Add(4 * time.Second)
	for i := 0; i < 100; i++ {
		m.step(tickFast)
	}
	if m.animating() || m.kaizenGrow() != 1 {
		t.Fatal("settled: nothing moves")
	}
	m.kaizenPage(1)
	m.kaizenPage(-1)
	if !m.animating() {
		t.Fatal("showing the Pulse page again shimmers the rewards")
	}
	m.kz.busy = true
	now = now.Add(time.Minute)
	if !m.animating() {
		t.Fatal("a load in flight turns the spinner")
	}
	st := New(Options{Static: true, Now: func() time.Time { return now }})
	setKaizen(st, kaizenDoc(t), pagePulse)
	st.kz.born, st.kz.shownAt = now, now
	if st.kaizenGrow() != 1 || st.kaizenShimmer() {
		t.Fatal("static rendering is settled")
	}
	if ensure := st.ensureTick(); ensure != nil {
		t.Fatal("static has no clock")
	}
}

func TestKaizenTilesCountUp(t *testing.T) {
	now := t0
	m := New(Options{NoColor: true, Now: func() time.Time { return now }})
	m.loading = false
	m.w, m.h = 140, 40
	m.view = viewKaizen
	m.kz.gen = 1
	m.applyKaizen(kaizenMsg{gen: 1, doc: kaizenDoc(t)})
	if v := m.counter["kz-landed"]; v.Settled() || v.Value() != 0 {
		t.Fatal("a load counts the tiles up from zero")
	}
	for i := 0; i < 100; i++ {
		m.step(tickFast)
	}
	if m.counter["kz-landed"].Value() != 20 || m.counter["kz-cost"].Value() != 620 {
		t.Fatalf("landed %v cost %v", m.counter["kz-landed"].Value(), m.counter["kz-cost"].Value())
	}
	before := m.kz.born
	now = now.Add(time.Hour)
	m.kz.gen = 2
	m.applyKaizen(kaizenMsg{gen: 2, doc: kaizenDoc(t)})
	if !m.kz.born.Equal(before) {
		t.Fatal("a load with the same numbers animates nothing")
	}
}

// ---- snapshot ---------------------------------------------------------------------

func TestSnapshotKaizen(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	b, err := os.ReadFile("testdata/kaizen-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "kaizen.json"), b, 0o644)
	os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"sessions":[],"malformed":0}`), 0o644)
	py, _ := filepath.Abs("../data/testdata/fakepy.sh")
	root, _ := filepath.Abs("../..")
	r := &data.Runner{Root: root, Python: py, ProjectsDir: dir, Timeout: 5 * time.Second}
	for view, want := range map[string]string{"kaizen": "[Pulse]", "kaizen-flow": "[Flow]", "kaizen-board": "[Board]"} {
		out, err := Snapshot(SnapshotOptions{Options: Options{Runner: r, NoColor: true, Range: "all", Now: func() time.Time { return goldenNow }}, View: view, W: 100, H: 30})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "[5 Kaizen]") || !strings.Contains(out, want) {
			t.Errorf("%s:\n%s", view, out)
		}
	}
}
