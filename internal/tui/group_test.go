package tui

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"isshoni/internal/data"
)

// tableLines are the dashboard's table rows (header first) as runes.
func tableLines(t *testing.T, m *model) [][]rune {
	t.Helper()
	var out [][]rune
	in := false
	for _, ln := range strings.Split(plain(m), "\n") {
		switch {
		case strings.Contains(ln, "REPO / TASK"):
			in = true
		case strings.HasPrefix(ln, "╰"):
			in = false
		}
		if in {
			if i := strings.Index(ln, "│ │"); i >= 0 {
				ln = ln[:i+1]
			}
			out = append(out, []rune(ln))
		}
	}
	if len(out) == 0 {
		t.Fatal("no table")
	}
	return out
}

func tableLine(t *testing.T, m *model, contains string) string {
	t.Helper()
	for _, ln := range tableLines(t, m) {
		if strings.Contains(string(ln), contains) {
			return string(ln)
		}
	}
	t.Fatalf("no table row containing %q", contains)
	return ""
}

// signalModel holds a repository with a bare session and a task of two sessions, all with signals,
// and a quiet repository of one session.
func signalModel(w, h int) *model {
	mk := func(id, repo, task string, main, sub int64) *data.Session {
		s := groupSession(id, repo, "/r/"+repo, task, 0, main, sub)
		s.Repeats, s.ReviewLoops, s.WarnCount, s.Malformed = 12, 3, 4, 7
		return s
	}
	quiet := groupSession("q1000000", "quiet", "/r/quiet", "", 0, 10, 0)
	quiet.Repeats, quiet.ReviewLoops, quiet.WarnCount, quiet.Malformed = 0, 0, 0, 0
	quiet.Recheck = data.Recheck{}
	return modelOf(w, h, mk("a1000000", "alpha", "", 100, 0), mk("a2000000", "alpha", "ci", 100, 300), mk("a3000000", "alpha", "ci", 100, 0), quiet)
}

func TestSignalColumnsLineUp(t *testing.T) {
	m := signalModel(140, 40)
	hdr := string(tableLines(t, m)[0])
	var ends []int
	for _, h := range []string{"SUB%", "DISP", "REP", "LOOP", "RCHK", "WARN", "BAD"} {
		i := strings.Index(hdr, " "+h+" ")
		if i < 0 {
			t.Fatalf("header lacks %s: %s", h, hdr)
		}
		ends = append(ends, len([]rune(hdr[:i+1+len(h)])))
	}
	for _, key := range []string{"alpha ×", "ci ×", "a1000000", "a2000000", "quiet"} {
		row := []rune(tableLine(t, m, key))
		for i, e := range ends {
			if row[e-1] == ' ' || row[e] != ' ' {
				t.Errorf("%s: column %d does not end at cell %d: %s", key, i, e, string(row))
			}
		}
	}
	if quiet := tableLine(t, m, "quiet"); strings.Count(quiet, "·") < 6 { // dot, REP, LOOP, RCHK, WARN, BAD
		t.Errorf("zero signals show a dot: %s", quiet)
	}
}

func TestBadColumnOnlyWithMalformed(t *testing.T) {
	m := groupedModel(140, 40)
	for _, s := range m.sessions {
		s.Malformed = 0
	}
	if strings.Contains(string(tableLines(t, m)[0]), "BAD") {
		t.Fatal("BAD column without malformed lines")
	}
	m.sessions["a1000000"].Malformed = 1
	if !strings.Contains(string(tableLines(t, m)[0]), "BAD") {
		t.Fatal("BAD column with a malformed line")
	}
}

func TestGroupRatesComeFromSummedCounts(t *testing.T) {
	a := groupSession("a1000000", "r", "/r", "t", 0, 100, 0)
	a.Recheck = data.Recheck{ExplorersWithCitations: 1, Rechecked: 1}
	b := groupSession("b1000000", "r", "/r", "t", 10, 100, 300)
	b.Recheck = data.Recheck{ExplorersWithCitations: 3, Rechecked: 0}
	m := modelOf(140, 40, a, b)
	row := tableLine(t, m, "t ×2")
	// subagents $0.00045 of $0.00065, 1 of 4 claims; the means of the sessions' rates would be 41% and 50%
	if !strings.Contains(row, "69%") || !strings.Contains(row, "25%") || strings.Contains(row, "41%") || strings.Contains(row, "50%") {
		t.Fatalf("group row: %s", row)
	}
}

func TestColumnsDropInOrder(t *testing.T) {
	m := groupedModel(140, 40)
	for _, s := range m.sessions {
		s.Malformed = 1
	}
	var prev tableCols
	var seen []string
	for iw := 40; iw <= 130; iw++ {
		c := m.tableCols(iw)
		if w := width(c.header("ACTIVE")); w > iw {
			t.Fatalf("iw %d: header is %d wide", iw, w)
		}
		state := ""
		for _, f := range []struct {
			on   bool
			name string
		}{{c.sig, "sig"}, {c.showTok, "tok"}, {c.subPct, "sub%"}, {c.bad, "bad"}} {
			if f.on {
				state += f.name + " "
			}
		}
		if len(seen) == 0 || seen[len(seen)-1] != state {
			seen = append(seen, state)
		}
		if prev.sig && !c.sig || prev.showTok && !c.showTok || prev.subPct && !c.subPct || prev.showStart && !c.showStart || prev.bad && !c.bad {
			t.Fatalf("iw %d: columns not monotonic: %+v after %+v", iw, c, prev)
		}
		if !strings.Contains(c.header("ACTIVE"), "COST") {
			t.Fatalf("iw %d: no COST column", iw)
		}
		prev = c
	}
	if c := m.tableCols(56); !c.sig || c.showStart { // a 60-column terminal
		t.Fatalf("the minimum terminal keeps the signals and drops the time: %+v", c)
	}
	if got, want := strings.Join(seen, "|"), "|sig |sig tok |sig tok sub% |sig tok sub% bad "; got != want {
		t.Fatalf("columns appear as %q, want %q", got, want)
	}
	if c := m.tableCols(200); c.split != 16 || c.name != maxName {
		t.Fatalf("slack goes to the bar then the name: %+v", c)
	}
}

func TestNoLineWiderThanTheTerminal(t *testing.T) {
	for w := 60; w <= 160; w++ {
		m := signalModel(w, 30)
		m.fixScroll()
		for i, line := range strings.Split(m.Render(), "\n") {
			if width(line) > w {
				t.Fatalf("%d wide: line %d is %d", w, i, width(line))
			}
		}
	}
}

func TestMergedRows(t *testing.T) {
	m := groupedModel(140, 40)
	by := map[string]treeRow{}
	for _, r := range m.rows() {
		by[r.name] = r
	}
	if r := by["zz"]; r.kind != kindTask || r.s == nil || r.s.ID != "a4000000" || r.suffix != "a4000000" || r.depth != 1 {
		t.Errorf("merged task: %+v", r)
	}
	if r := by["beta"]; r.kind != kindRepo || r.s == nil || r.suffix != "b1000000" || r.depth != 0 {
		t.Errorf("merged repository: %+v", r)
	}
	if r := by["alpha"]; r.s != nil || r.suffix != "×4" {
		t.Errorf("repository: %+v", r)
	}
	if r := by["ci"]; r.s != nil || r.suffix != "×2" {
		t.Errorf("task: %+v", r)
	}
	if repos, tasks := shownGroups(m.rows()); repos != 2 || tasks != 2 {
		t.Errorf("groups %d repos, %d tasks", repos, tasks)
	}

	one := modelOf(140, 40, groupSession("t1000000", "solo", "/r/solo", "job", 0, 100, 0))
	if got := rowNames(one); got != "all ×1,solo ×1,  job t1000000" {
		t.Errorf("one tasked session: %s", got)
	}

	m.selID = "a4000000"
	send(m, "enter")
	if m.view != viewSession || m.selID != "a4000000" {
		t.Errorf("enter on a merged row: view %v, session %s", m.view, m.selID)
	}
}

func TestGroupLiveDot(t *testing.T) {
	m := groupedModel(140, 40)
	dot := func(row string) bool { return strings.ContainsAny(tableLine(t, m, row), "●○") }
	if dot("ci ×2") {
		t.Fatal("no live session, no dot")
	}
	m.seen["a2000000"] = m.now().Add(-5 * time.Second)
	if !dot("ci ×2") {
		t.Fatal("a live session lights its group")
	}
}

// totalsJSON is the part of a group's totals in `isshoni sessions --json` the dashboard shows.
type totalsJSON struct {
	Sessions      []string     `json:"sessions"`
	Tokens        data.Tokens  `json:"tokens"`
	Cost          data.Cost    `json:"cost"`
	DispatchCount int          `json:"dispatch_count"`
	Repeats       int          `json:"repeats"`
	ReviewLoops   int          `json:"review_loops"`
	Recheck       data.Recheck `json:"recheck"`
	WarnCount     int          `json:"warn_count"`
	Malformed     int          `json:"malformed"`
}

func TestGroupTotalsMatchTheJSON(t *testing.T) {
	root, _ := filepath.Abs("../..")
	for _, fx := range []string{"projects", "grouping"} {
		cmd := exec.Command("python3", "-m", "isshoni", "sessions", "--json", "--projects-dir", filepath.Join(root, "tests", "fixtures", fx),
			"--prices", filepath.Join(root, "tests", "fixtures", "prices.toml"))
		cmd.Dir = root
		var so bytes.Buffer
		cmd.Stdout = &so
		if err := cmd.Run(); errors.Is(err, exec.ErrNotFound) {
			t.Skipf("python unavailable: %v", err)
		} else if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Sessions []*data.Session `json:"sessions"`
			Repos    []struct {
				totalsJSON
				Repo  string       `json:"repo"`
				Tasks []totalsJSON `json:"tasks"`
			} `json:"repos"`
		}
		if err := json.Unmarshal(so.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if len(out.Repos) == 0 {
			t.Fatalf("%s: no repos", fx)
		}
		byID := map[string]*data.Session{}
		for _, s := range out.Sessions {
			byID[s.ID] = s
		}
		check := func(name string, j totalsJSON) {
			var ss []*data.Session
			for _, id := range j.Sessions {
				ss = append(ss, byID[id])
			}
			g := sumTotals(ss)
			near := func(a, b float64) bool { return math.Abs(a-b) < 1e-5 } // the JSON rounds money to 6 decimals
			if g.tokens != j.Tokens.Total || !near(g.cost, j.Cost.Total) || !near(g.mainCost, j.Cost.Main) || !near(g.subCost, j.Cost.Subagents) ||
				g.unpriced != j.Cost.UnpricedTokens || !slices.Equal(g.unpricedModels, j.Cost.UnpricedModels) ||
				g.dispatches != j.DispatchCount || g.repeats != j.Repeats || g.loops != j.ReviewLoops ||
				g.rechecked != j.Recheck.Rechecked || g.candidates != j.Recheck.ExplorersWithCitations ||
				g.warns != j.WarnCount || g.malformed != j.Malformed {
				t.Errorf("%s/%s: go sums %+v, json %+v", fx, name, g, j)
			}
			if g.cost > 0 {
				if got := share(g.subCost, g.cost); got != pct(j.Cost.SubagentPct) {
					t.Errorf("%s/%s: SUB%% %s, json %s", fx, name, got, pct(j.Cost.SubagentPct))
				}
			}
			if j.Recheck.Rate != nil {
				if got := share(float64(g.rechecked), float64(g.candidates)); got != pct(100**j.Recheck.Rate) {
					t.Errorf("%s/%s: RCHK %s, json %s", fx, name, got, pct(100**j.Recheck.Rate))
				}
			} else if g.candidates != 0 {
				t.Errorf("%s/%s: json has no rate but %d candidates", fx, name, g.candidates)
			}
		}
		for _, r := range out.Repos {
			check(r.Repo, r.totalsJSON)
			for _, task := range r.Tasks {
				check(r.Repo+"/"+strings.Join(task.Sessions, ","), task)
			}
		}
	}
}
