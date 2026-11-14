package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"isshoni/internal/data"
)

// pluginModel is groupedModel where some subagent cost belongs to plugin agents (factory:coder, factory:reviewer).
func pluginModel(w, h int) *model {
	m := groupedModel(w, h)
	split := func(s *data.Session, role string) {
		u := s.ByAgent[role]
		half := u
		half.Cost = u.Cost / 2
		s.ByAgent[role] = half
		s.ByAgent["factory:"+role] = half
	}
	split(m.sessions["a2000000"], "coder")
	split(m.sessions["a3000000"], "explorer")
	m.sessions["a3000000"].ByAgent["factory:reviewer"] = data.Usage{Cost: 0}
	return m
}

func rowByKey(m *model, key string) treeRow {
	for _, r := range m.rows() {
		if r.key == key {
			return r
		}
	}
	return treeRow{}
}

// rowNamed finds a group row (no session) by kind and name.
func rowNamed(t *testing.T, m *model, kind rowKind, name string) treeRow {
	t.Helper()
	for _, r := range m.rows() {
		if r.kind == kind && r.name == name {
			return r
		}
	}
	t.Fatalf("no row %q", name)
	return treeRow{}
}

func selectRow(m *model, key string) {
	m.selID = key
	m.fixScroll()
}

// sums totals a group's agent roles, models and cost without sumTotals.
func sums(group []*data.Session) (byRole, byModel map[string]float64, cost float64) {
	byRole, byModel = map[string]float64{}, map[string]float64{}
	for _, s := range group {
		cost += s.Cost.Total
		for k, u := range s.ByAgent {
			byRole[agentRole(k)] += u.Cost
		}
		for k, u := range s.ByModel {
			byModel[k] += u.Cost
		}
	}
	return
}

func lineWith(t *testing.T, out string, parts ...string) string {
	t.Helper()
	for _, ln := range strings.Split(out, "\n") {
		ok := true
		for _, p := range parts {
			ok = ok && strings.Contains(ln, p)
		}
		if ok {
			return ln
		}
	}
	t.Fatalf("no line with %q in\n%s", parts, out)
	return ""
}

// TestMixIsTheRowUnderTheCursor: whatever row the cursor is on, the panel titles name it and every
// figure in the panels is the sum over that row's sessions, the same sum as the row's COST cell.
func TestMixIsTheRowUnderTheCursor(t *testing.T) {
	for name, mk := range map[string]func(w, h int) *model{"grouped": groupedModel, "plugin": pluginModel} {
		for _, sz := range [][2]int{{140, 40}, {100, 30}} {
			m := mk(sz[0], sz[1])
			for _, r := range m.rows() {
				selectRow(m, r.key)
				out := plain(m)
				byRole, byModel, cost := sums(r.group)
				var agentSum, modelSum float64
				for _, v := range byRole {
					agentSum += v
				}
				for _, v := range byModel {
					modelSum += v
				}
				if d := agentSum - cost; d > 1e-9 || d < -1e-9 {
					t.Fatalf("%s %s: agent costs %v != row cost %v", name, r.label(), agentSum, cost)
				}
				if d := modelSum - cost; d > 1e-9 || d < -1e-9 {
					t.Fatalf("%s %s: model costs %v != row cost %v", name, r.label(), modelSum, cost)
				}
				if got := lineWith(t, out, ">"); !strings.Contains(got, Money(cost, "USD")) {
					t.Fatalf("%s %s: the row's COST cell is not %s: %s", name, r.label(), Money(cost, "USD"), got)
				}
				where := fmt.Sprintf("%s %dx%d %s", name, sz[0], sz[1], r.label())
				if sz[0] >= 120 {
					for _, title := range []string{"Agent mix · ", "Model mix · "} {
						if !strings.Contains(out, title+r.label()) {
							t.Fatalf("%s: no title %q", where, title+r.label())
						}
					}
				} else if !strings.Contains(out, " mix · "+r.label()+" · "+Money(cost, "USD")) {
					t.Fatalf("%s: the heading does not name the row", where)
				}
				if sz[0] >= 120 {
					for role, v := range byRole {
						want := Money(v, "USD") + " " + pct(100*v/cost)
						if ln := lineWith(t, out, "● "+role); !strings.Contains(strings.Join(strings.Fields(ln), " "), want) {
							t.Fatalf("%s: agent %s: want %q in %q", where, role, want, ln)
						}
					}
					for k, v := range byModel {
						want := Money(v, "USD") + " " + pct(100*v/cost)
						ln := lineWith(t, out, shortModel(k))
						if got := strings.Join(strings.Fields(ln), " "); !strings.Contains(got, want) {
							t.Fatalf("%s: model %s: want %q in %q", where, k, want, ln)
						}
					}
				} else {
					entries := map[string]string{}
					for role, v := range byRole {
						entries[role] = Money(v, "USD") + " " + pct(100*v/cost)
					}
					checkStrip(t, where+" agents", lineWith(t, out, "agents "), entries)
					entries = map[string]string{}
					for k, v := range byModel {
						entries[shortModel(k)] = Money(v, "USD") + " " + pct(100*v/cost)
					}
					checkStrip(t, where+" models", lineWith(t, out, "models "), entries)
				}
			}
		}
	}
}

// checkStrip: the strip holds `name money share` for entries, and says how many it left out with +N.
func checkStrip(t *testing.T, where, ln string, entries map[string]string) {
	t.Helper()
	present := 0
	for name, want := range entries {
		if strings.Contains(ln, name+" "+want) {
			present++
		}
	}
	if present == 0 {
		t.Fatalf("%s: the first entry is missing: %q", where, ln)
	}
	if out := len(entries) - present; out > 0 && !strings.Contains(ln, fmt.Sprintf(" +%d", out)) {
		t.Fatalf("%s: %d entries left out without a matching +N: %q", where, out, ln)
	} else if out == 0 && strings.Contains(ln, " +") {
		t.Fatalf("%s: a +N marker with nothing left out: %q", where, ln)
	}
}

func TestPluginAgentsMergeIntoTheirRole(t *testing.T) {
	m := pluginModel(140, 40)
	m.agents = &data.Agents{Agents: []data.AgentRow{{Agent: "coder"}, {Agent: "factory:coder"}}}
	selectRow(m, "a2000000")
	s := m.sessions["a2000000"]
	want := Money(s.ByAgent["coder"].Cost+s.ByAgent["factory:coder"].Cost, "USD")
	out := plain(m)
	if n := strings.Count(out, "● coder"); n != 1 {
		t.Fatalf("%d coder lines", n)
	}
	if ln := lineWith(t, out, "● coder"); !strings.Contains(ln, want) {
		t.Fatalf("coder money is not the sum %s: %s", want, ln)
	}
	if strings.Contains(out, "factory:coder") {
		t.Fatal("a plugin agent keeps its own line")
	}
	send(m, "3")
	out = plain(m)
	if !strings.Contains(out, "factory:coder") || strings.Count(out, "coder") < 2 {
		t.Fatalf("the Agents view lists both:\n%s", out)
	}
}

func TestCursorSurvivesRefresh(t *testing.T) {
	for _, pick := range []struct {
		kind rowKind
		name string
	}{{kindTask, "ci"}, {kindRepo, "alpha"}} {
		m := groupedModel(120, 40)
		row := rowNamed(t, m, pick.kind, pick.name)
		selectRow(m, row.key)
		check := func(step string) {
			t.Helper()
			cur, _ := m.cursorRow(m.rows())
			if r := m.rows()[cur]; m.selID != row.key || r.key != row.key || r.name != pick.name || len(r.group) != len(row.group) {
				t.Fatalf("%s %s: selID %q, cursor on %s", pick.name, step, m.selID, r.label())
			}
		}
		send(m, "s")
		check("re-sort")

		changed := *m.sessions["a2000000"]
		changed.Cost.Total *= 3
		changed.LastChange = changed.LastChange.Add(time.Minute)
		m.mergeSession(&changed)
		m.normalizeSelection()
		check("mergeSession")

		m.sortBy = sortRecent
		fresh := groupSession("z1000000", "zeta", "/r/zeta", "", 500, 100, 0)
		fresh.LastChange = fresh.End.Add(time.Minute)
		m.mergeSession(fresh)
		m.normalizeSelection()
		if m.rows()[1].name != "zeta" {
			t.Fatalf("the new session should sort first: %s", rowNames(m))
		}
		check("new session above")

		var all []data.Session
		for _, s := range m.sessions {
			all = append(all, *s)
		}
		m.mergeLoad(sessionsMsg{res: &data.SessionsResult{Sessions: all}})
		m.normalizeSelection()
		check("mergeLoad")
	}
}

func TestSelectionFallsBackToTheAllRow(t *testing.T) {
	m := groupedModel(120, 40)
	selectRow(m, rowNamed(t, m, kindTask, "ci").key)
	send(m, "/", "b", "e", "t", "a")
	if r := m.rows()[mustCursor(m)]; r.kind != kindAll || m.selID != r.key {
		t.Fatalf("the filter hides ci: cursor on %s", r.label())
	}
	send(m, "backspace", "backspace", "backspace", "backspace")
	if r := m.rows()[mustCursor(m)]; r.kind != kindAll {
		t.Fatalf("clearing the filter keeps the all row, cursor on %s", r.label())
	}
}

func TestEnterOpensSessionRowsOnly(t *testing.T) {
	m := groupedModel(120, 40)
	for _, r := range m.rows() {
		m.view = viewDashboard
		selectRow(m, r.key)
		send(m, "enter")
		switch {
		case r.s != nil && (m.view != viewSession || m.selID != r.s.ID):
			t.Errorf("enter on %s: view %v, selID %q", r.label(), m.view, m.selID)
		case r.s == nil && m.view != viewDashboard:
			t.Errorf("enter on %s opened the %v view", r.label(), m.view)
		}
	}
	m.view = viewDashboard
	selectRow(m, rowNamed(t, m, kindRepo, "alpha").key)
	send(m, "tab")
	if m.view != viewAgents {
		t.Errorf("tab from a group row goes to %v, not past the session view", m.view)
	}
}

func TestFollowFromAGroupRow(t *testing.T) {
	m := groupedModel(120, 40)
	selectRow(m, rowNamed(t, m, kindTask, "ci").key)
	m.follow = true
	m.followTarget()
	if m.selID != "b1000000" {
		t.Fatalf("follow moves to the most recent session, got %q", m.selID)
	}
	send(m, "k")
	if m.follow {
		t.Fatal("a cursor move turns follow off")
	}
}

func TestLayoutStaysPutAsTheCursorWalks(t *testing.T) {
	for _, sz := range [][2]int{{140, 40}, {120, 24}, {100, 30}} {
		m := pluginModel(sz[0], sz[1])
		var lines, tableEnd int
		for i, r := range m.rows() {
			selectRow(m, r.key)
			out := strings.Split(plain(m), "\n")
			end := -1
			for j, ln := range out {
				if strings.HasPrefix(ln, "╰") && end < 0 {
					end = j
				}
			}
			if i == 0 {
				lines, tableEnd = len(out), end
			}
			if len(out) != lines || end != tableEnd {
				t.Fatalf("%dx%d on %s: %d lines, table ends at %d; want %d and %d", sz[0], sz[1], r.label(), len(out), end, lines, tableEnd)
			}
		}
	}
}

func TestTilesAreRangeWideAndCaptioned(t *testing.T) {
	m := groupedModel(140, 40)
	tiles := func() string {
		out := strings.Split(plain(m), "\n")
		for i, ln := range out {
			if strings.Contains(ln, "COST") && strings.Contains(ln, "SUBAGENT SHARE") {
				return strings.Join(out[i:i+3], "\n")
			}
		}
		t.Fatal("no tiles")
		return ""
	}
	want := tiles()
	for _, r := range m.rows() {
		selectRow(m, r.key)
		if got := tiles(); got != want {
			t.Fatalf("tiles change on %s:\n%s\nwant\n%s", r.label(), got, want)
		}
		if !strings.Contains(plain(m), " ↑ totals of all 5 sessions shown · the mix panels show the selected row") {
			t.Fatalf("no caption on %s", r.label())
		}
	}
	m = groupedModel(60, 24)
	for _, ln := range strings.Split(plain(m), "\n") {
		if strings.Contains(ln, "↑ totals of all 5") && width(ln) <= 60 {
			return
		}
	}
	t.Fatal("no caption that fits 60 columns")
}

func TestNoCaptionWithoutSessions(t *testing.T) {
	out := plain(modelOf(140, 40))
	if strings.Contains(out, "totals of all") {
		t.Fatal("an empty table has no caption")
	}
}
