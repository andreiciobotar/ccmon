package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ccmon/internal/data"
)

// ---- fixtures ---------------------------------------------------------------------

func ftask(slug, phase string) data.FactoryTask {
	return data.FactoryTask{Slug: slug, Phase: phase, Base: "origin/main", PRState: "none", Session: "none"}
}

// fstatus is a status whose counts follow its rows.
func fstatus(limit int, rows ...data.FactoryTask) *data.FactoryStatus {
	st := &data.FactoryStatus{Rows: rows, Cap: limit}
	for _, r := range rows {
		switch r.Phase {
		case "motion":
			st.InMotion++
		case "review":
			st.InReview++
		case "merged":
			st.Merged++
		}
	}
	return st
}

// manyTasks is n tasks in motion.
func manyTasks(n int) []data.FactoryTask {
	var rows []data.FactoryTask
	for i := 0; i < n; i++ {
		x := ftask(fmt.Sprintf("task-%02d", i), "motion")
		x.Session, x.Elapsed = "working", ptr(int64(60*(i+1)))
		rows = append(rows, x)
	}
	return rows
}

// alpha has every phase, an overtime and a stale task, a stacked one, and a closed pull request.
func alpha() factoryRepo {
	quiet, login, stacked := ftask("quiet-refactor", "motion"), ftask("login-flow", "motion"), ftask("stacked-on-login", "motion")
	quiet.Session, quiet.Elapsed = "working", ptr(int64(252))
	login.Session, login.Elapsed, login.Overtime = "working", ptr(int64(8100)), true
	stacked.Base, stacked.Elapsed, stacked.PRState = "worktree-login-flow", ptr(int64(95)), "unknown"
	limits, docs, old := ftask("api-rate-limits", "review"), ftask("rate-limit-docs", "review"), ftask("old-experiment", "merged")
	limits.PRState, limits.PRURL, limits.Elapsed = "open", "https://github.com/o/alpha/pull/42", ptr(int64(5400))
	docs.PRState, docs.PRURL, docs.Stale = "closed", "https://github.com/o/alpha/pull/40", true
	old.PRState, old.PRURL, old.Elapsed = "merged", "https://github.com/o/alpha/pull/31", ptr(int64(61))
	return factoryRepo{name: "alpha", path: "/r/alpha", st: fstatus(5, stacked, old, docs, quiet, limits, login)}
}

func fakeFactoryRepos() []factoryRepo {
	return []factoryRepo{
		alpha(),
		{name: "beta-é日本語", path: "/r/beta", st: fstatus(2, manyTasks(3)...)},
		{name: "gamma", path: "/r/gamma", err: "not inside a git repository"},
	}
}

// setFactory puts the model on the factory view holding repos as the last result.
func setFactory(m *model, repos ...factoryRepo) {
	m.view = viewFactory
	m.fac.repos, m.fac.doneAt, m.fac.cap = repos, m.now(), 5
	for _, r := range repos {
		if r.st != nil {
			m.fac.cap = r.st.Cap
			break
		}
	}
	m.fixScroll()
}

func factoryModel(noColor bool, w, h int, repos ...factoryRepo) *model {
	m := fakeModel(noColor, w, h)
	setFactory(m, repos...)
	return m
}

// ---- rendering --------------------------------------------------------------------

func TestFactoryRenderVariants(t *testing.T) {
	unknown := ftask("waiting-on-human", "blocked")
	unknown.Session = "unknown"
	variants := map[string]func(m *model){
		"several": func(m *model) {
			setFactory(m, append(fakeFactoryRepos(), factoryRepo{name: "delta", path: "/r/delta", st: fstatus(5)})...)
		},
		"over cap": func(m *model) {
			setFactory(m, factoryRepo{name: "busy", path: "/r/busy", st: fstatus(5, manyTasks(30)...)})
		},
		"cap 40": func(m *model) {
			setFactory(m, factoryRepo{name: "big", path: "/r/big", st: fstatus(40, manyTasks(12)...)})
		},
		"unknown": func(m *model) {
			setFactory(m, factoryRepo{name: "odd", path: "/r/odd", st: fstatus(5, unknown, ftask("a", "motion"))})
		},
		"editing": func(m *model) { setFactory(m, fakeFactoryRepos()...); m.fac.editing, m.fac.field = true, "12" },
		"edit err": func(m *model) {
			setFactory(m, fakeFactoryRepos()...)
			m.fac.editing, m.fac.fieldErr = true, "enter a whole number from 1 to 999"
		},
		"cap error": func(m *model) {
			setFactory(m, fakeFactoryRepos()...)
			m.fac.capErr = "/h/.claude/factory/settings.json is not a JSON object"
		},
		"empty":   func(m *model) { setFactory(m) },
		"waiting": func(m *model) { m.view = viewFactory },
		"toast":   func(m *model) { setFactory(m, fakeFactoryRepos()...); m.setToast("worktree cap set to 8", false) },
		"long": func(m *model) {
			setFactory(m, factoryRepo{name: strings.Repeat("長い", 40), path: "/r/x", st: fstatus(5, ftask(strings.Repeat("s", 90), "motion"))})
		},
	}
	for name, setup := range variants {
		for _, sz := range sizes {
			for _, nc := range []bool{false, true} {
				m := fakeModel(nc, sz[0], sz[1])
				setup(m)
				checkFrame(t, fmt.Sprintf("factory %s %dx%d nc=%v", name, sz[0], sz[1], nc), m, nc)
			}
		}
	}
}

func TestFactoryMeter(t *testing.T) {
	cases := []struct {
		limit, n int
		want     string
	}{
		{5, 0, "in motion ○○○○○ 0/5"},
		{5, 3, "in motion ●●●○○ 3/5"},
		{5, 5, "in motion ●●●●● 5/5"},
		{2, 3, "in motion ●●● 3/2"},
	}
	for _, c := range cases {
		m := factoryModel(true, 140, 40, factoryRepo{name: "r", path: "/r", st: fstatus(c.limit, manyTasks(c.n)...)})
		if out := plain(m); !strings.Contains(out, c.want) {
			t.Errorf("cap %d, %d in motion: want %q in\n%s", c.limit, c.n, c.want, out)
		}
	}
	m := factoryModel(true, 140, 40, factoryRepo{name: "r", path: "/r", st: fstatus(40, manyTasks(10)...)})
	if out := plain(m); !strings.Contains(out, "in motion ██████████░░░░░░░░░░ 10/40") && !strings.Contains(out, "in motion █████░░░░░░░░░░░░░░░ 10/40") {
		t.Errorf("a cap above %d is a %d-cell bar:\n%s", meterCells, meterCells, out)
	}
	m = factoryModel(false, 140, 40, factoryRepo{name: "r", path: "/r", st: fstatus(5, manyTasks(3)...)})
	mid := m.th.on(m.th.Bg).t("●●●", m.th.Green)
	m.fac.repos[0].st = fstatus(5, manyTasks(5)...)
	full := m.th.on(m.th.Bg).t("●●●●●", m.th.Yellow)
	m.fac.repos = append(m.fac.repos, factoryRepo{name: "s", path: "/s", st: fstatus(2, manyTasks(3)...)})
	over := m.th.on(m.th.Bg).t("●●●", m.th.Red)
	out := m.Render()
	if !strings.Contains(out, full) || !strings.Contains(out, over) || strings.Contains(out, mid) {
		t.Errorf("filled cells: yellow at the cap, red above it")
	}
}

func TestFactoryTableColumnsAndPhaseLabels(t *testing.T) {
	out := plain(factoryModel(true, 140, 40, alpha()))
	for _, want := range []string{"SLUG", "PHASE", "ELAPSED", "PR", "BASE", "FLAGS", "in motion", "waiting for review", "merged, not cleaned",
		"#42", "#40 closed", "#31", "↳ login-flow", "[overtime]", "[stale]", "4m12s", "2h15m", "1h30m", "origin/main"} {
		if want == "origin/main" {
			continue // the prefix is dropped
		}
		if !strings.Contains(out, want) {
			t.Errorf("140 columns lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "origin/") {
		t.Errorf("the origin/ prefix of a base is dropped:\n%s", out)
	}
	short := plain(factoryModel(true, 80, 24, alpha()))
	if strings.Contains(short, "waiting for review") || !strings.Contains(short, "◆ review ") || !strings.Contains(short, "⠋ motion") || !strings.Contains(short, "Factory  3 motion · 2 review · 1 merged") {
		t.Errorf("below 100 columns the labels are short:\n%s", short)
	}
	narrow := plain(factoryModel(true, 60, 20, alpha()))
	for _, gone := range []string{"BASE", "PR "} {
		if strings.Contains(narrow, gone) {
			t.Errorf("60 columns should have dropped %q:\n%s", gone, narrow)
		}
	}
	for _, kept := range []string{"SLUG", "PHASE", "FLAGS", "[overtime]"} {
		if !strings.Contains(narrow, kept) {
			t.Errorf("60 columns lack %q:\n%s", kept, narrow)
		}
	}
}

func TestFactoryColumnsAlign(t *testing.T) {
	var starts []int
	for _, l := range strings.Split(plain(factoryModel(true, 140, 40, fakeFactoryRepos()...)), "\n") {
		if i := strings.Index(l, "PHASE"); i >= 0 {
			starts = append(starts, len([]rune(l[:i])))
		}
	}
	if len(starts) != 2 || starts[0] != starts[1] {
		t.Fatalf("the tables of two repositories share their columns: %v", starts)
	}
}

func TestFactoryRowOrder(t *testing.T) {
	late, early, mid := ftask("zz", "motion"), ftask("aa", "motion"), ftask("mm", "motion")
	late.Overtime = true
	rows := sortedTasks([]data.FactoryTask{ftask("merged-one", "merged"), ftask("q", "zeta"), early, ftask("r", "review"), late, mid, ftask("p", "alpha")})
	var got []string
	for _, r := range rows {
		got = append(got, r.Slug)
	}
	if want := "zz aa mm r merged-one p q"; strings.Join(got, " ") != want {
		t.Fatalf("order %q, want %q", strings.Join(got, " "), want)
	}
}

func TestFactoryCells(t *testing.T) {
	pr := func(phase, state, url string) string {
		x := ftask("x", phase)
		x.PRState, x.PRURL = state, url
		return prText(x)
	}
	u := "https://github.com/o/r/pull/7"
	for got, want := range map[string]string{
		pr("motion", "none", ""): "-", pr("motion", "unknown", ""): "?", pr("motion", "open", u): "#7 open", pr("review", "open", u): "#7",
		pr("review", "closed", u): "#7 closed", pr("merged", "merged", u): "#7", pr("motion", "open", ""): "open",
	} {
		if got != want {
			t.Errorf("pr cell %q, want %q", got, want)
		}
	}
	if b, s := baseText("worktree-login-flow"); b != "↳ login-flow" || !s {
		t.Errorf("stacked base %q %v", b, s)
	}
	if b, s := baseText("origin/main"); b != "main" || s {
		t.Errorf("default base %q %v", b, s)
	}
	m := factoryModel(true, 140, 40, factoryRepo{name: "r", path: "/r", st: fstatus(5, ftask("no-time", "motion"))})
	if !strings.Contains(plain(m), "no-time") || !strings.Contains(plain(m), "-  -") {
		t.Errorf("a task without an elapsed time shows -:\n%s", plain(m))
	}
}

func TestFactoryEmptyAndFirstResult(t *testing.T) {
	m := factoryModel(true, 100, 30)
	out := plain(m)
	for _, want := range []string{"No factory tasks here yet", "/factory:dispatch", "worktree cap", "c edit", "~/.claude/factory/settings.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty state lacks %q:\n%s", want, out)
		}
	}
	m = fakeModel(true, 100, 30)
	m.view = viewFactory
	if out := plain(m); !strings.Contains(out, "asking the factory…") || strings.Contains(out, "No factory tasks") {
		t.Errorf("before the first result:\n%s", out)
	}
}

func TestFactorySubheaderAndSettingsLine(t *testing.T) {
	m := factoryModel(true, 140, 40, fakeFactoryRepos()...)
	out := plain(m)
	for _, want := range []string{"Factory  6 in motion · 2 waiting for review · 1 merged, not cleaned  · as of 11:00:00", "worktree cap  5  per repo", "⚠ not inside a git repository", "in motion ●●● 3/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	m.fac.busy = true
	if out := plain(m); !strings.Contains(out, "refreshing") || strings.Contains(out, "as of") {
		t.Errorf("a refresh in flight shows refreshing:\n%s", out)
	}
	m.fac.capErr = "/h/settings.json is not a JSON object"
	if out := plain(m); !strings.Contains(out, "worktree cap  /h/settings.json is not a JSON object") {
		t.Errorf("a broken settings file shows its error instead of a number:\n%s", out)
	}
	m.fac.capErr, m.fac.editing, m.fac.field = "", true, "12"
	if out := plain(m); !strings.Contains(out, "[ 12█ ]") || !strings.Contains(out, "enter save · esc cancel · ↑↓ adjust") || !strings.Contains(out, "save") {
		t.Errorf("editing:\n%s", out)
	}
}

func TestFactoryScroll(t *testing.T) {
	m := factoryModel(true, 80, 24, factoryRepo{name: "busy", path: "/r", st: fstatus(40, manyTasks(30)...)})
	if out := plain(m); !strings.Contains(out, "more lines · ↓ to scroll") {
		t.Fatalf("a cut body says so:\n%s", out)
	}
	send(m, "j")
	if m.fac.top != 1 {
		t.Fatalf("j scrolls one line, top=%d", m.fac.top)
	}
	send(m, "k", "k")
	if m.fac.top != 0 {
		t.Fatalf("top stays at 0, got %d", m.fac.top)
	}
	send(m, "G")
	out := plain(m)
	if strings.Contains(out, "more lines") || !strings.Contains(out, "task-29") || strings.Contains(out, "task-00") {
		t.Fatalf("G shows the end:\n%s", out)
	}
	send(m, "j")
	if want := m.factoryPositions() - 1; m.fac.top != want {
		t.Fatalf("scrolling stops at the end: top=%d want %d", m.fac.top, want)
	}
	send(m, "g")
	if m.fac.top != 0 {
		t.Fatal("g goes to the top")
	}
	m.w, m.h = 80, 60
	m.fac.top = 3
	m.fixScroll()
	if m.fac.top != 0 {
		t.Fatalf("a taller window pulls the scroll back, top=%d", m.fac.top)
	}
}

func TestFactoryAnimatesOnlyWhileSomethingMoves(t *testing.T) {
	m := factoryModel(true, 100, 30, alpha())
	if !m.animating() {
		t.Fatal("a working task turns its spinner")
	}
	quiet := ftask("quiet", "motion")
	m.fac.repos = []factoryRepo{{name: "q", path: "/q", st: fstatus(5, quiet)}}
	if m.animating() {
		t.Fatal("nothing moves")
	}
	m.fac.busy = true
	if !m.animating() {
		t.Fatal("a refresh in flight turns its spinner")
	}
	m.view = viewDashboard
	m.fac.repos = []factoryRepo{alpha()}
	if m.animating() {
		t.Fatalf("working tasks animate only on the factory view")
	}
}

func TestFactoryViewKeys(t *testing.T) {
	m := fakeModel(true, 120, 40)
	for _, want := range []viewID{viewSession, viewAgents, viewFactory, viewDashboard} {
		send(m, "tab")
		if m.view != want {
			t.Fatalf("tab -> %v, want %v", m.view, want)
		}
	}
	send(m, "shift+tab")
	if m.view != viewFactory {
		t.Fatalf("shift+tab from the dashboard -> %v", m.view)
	}
	send(m, "1", "4")
	if m.view != viewFactory {
		t.Fatal("4 opens the factory view")
	}
	send(m, "esc")
	if m.view != viewDashboard {
		t.Fatal("esc goes back to the dashboard")
	}
	if !strings.Contains(plain(m), "4 Factory") {
		t.Fatal("the tab is in the header")
	}

	bare := New(Options{Static: true})
	bare.loading = false
	for _, want := range []viewID{viewAgents, viewFactory, viewDashboard} {
		send(bare, "tab")
		if bare.view != want || bare.toast.text != "" {
			t.Fatalf("without a session tab skips the session view: got %v, want %v, toast %q", bare.view, want, bare.toast.text)
		}
	}
	send(bare, "shift+tab", "shift+tab", "shift+tab")
	if bare.view != viewDashboard {
		t.Fatalf("shift+tab skips it too: %v", bare.view)
	}
}

func TestFactoryHelpAndFooter(t *testing.T) {
	m := factoryModel(true, 120, 40, alpha())
	foot := plain(m)
	for _, want := range []string{"↑↓/jk move", "c edit cap", "r range", "tab next view", "? help", "q quit"} {
		if !strings.Contains(foot, want) {
			t.Errorf("footer lacks %q", want)
		}
	}
	m.fac.editing = true
	if foot := plain(m); !strings.Contains(foot, "enter save  ·  esc cancel") {
		t.Errorf("editing footer:\n%s", foot)
	}
	m.fac.editing, m.help = false, true
	out := plain(m)
	for _, want := range []string{"1 2 3 4", "dashboard · session · agents · factory", "c", "edit cap", "refreshes every 30 s while shown"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
}

// ---- goldens and screenshots ------------------------------------------------------

func TestGoldenFactory(t *testing.T) {
	shots := os.Getenv("CCMON_SHOT_DIR")
	for _, c := range []struct {
		name string
		w, h int
		set  func(m *model)
		gold bool
	}{
		{"factory-140x40", 140, 40, func(m *model) { setFactory(m, fakeFactoryRepos()...) }, true},
		{"factory-100x30", 100, 30, func(m *model) { setFactory(m, fakeFactoryRepos()...) }, true},
		{"factory-80x24", 80, 24, func(m *model) { setFactory(m, fakeFactoryRepos()...) }, true},
		{"factory-empty-80x24", 80, 24, func(m *model) { setFactory(m) }, true},
		{"factory-edit-100x30", 100, 30, func(m *model) { setFactory(m, fakeFactoryRepos()...); m.fac.editing, m.fac.field = true, "12" }, false},
		{"factory-busy-140x40", 140, 40, func(m *model) {
			busy := alpha()
			busy.name = "ccmon"
			setFactory(m, busy, factoryRepo{name: "agent", path: "/r/agent", st: fstatus(5, manyTasks(7)...)}, factoryRepo{name: "zahn-os", path: "/r/z", st: fstatus(40, manyTasks(12)...)})
		}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if shots != "" {
				m := fakeModel(false, c.w, c.h)
				c.set(m)
				if err := os.WriteFile(filepath.Join(shots, c.name+".ansi"), []byte(m.Render()+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !c.gold {
				return
			}
			m := fakeModel(true, c.w, c.h)
			c.set(m)
			got := m.Render() + "\n"
			path := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Fatalf("%s changed; run go test ./internal/tui -update and review the diff\n--- got ---\n%s", c.name, got)
			}
		})
	}
}

// ---- update loop with the stub ----------------------------------------------------

// factoryStubModel has two repositories with a worktrees directory and one without.
func factoryStubModel(t *testing.T) (*model, string, *time.Time) {
	t.Helper()
	m, dir := stubModel(t)
	t.Setenv("HOME", t.TempDir())
	for id, name := range map[string]string{sidA: "one", sidB: "two", "cccccccc-0003": "three"} {
		repo := filepath.Join(t.TempDir(), name)
		if name != "three" {
			if err := os.MkdirAll(filepath.Join(repo, ".claude", "worktrees"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		m.sessions[id].RepoPath = repo
	}
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	return m, dir, &now
}

func factoryCalls(t *testing.T, dir string) int { return calls(t, dir, "factory ") }

func TestFactoryEnteringStartsOneRefresh(t *testing.T) {
	m, dir, _ := factoryStubModel(t)
	got := only[factoryMsg](upd(m, press("4")))
	if len(got) != 1 || !m.fac.busy || factoryCalls(t, dir) != 2 {
		t.Fatalf("entering asks each repository with a worktrees directory once: %d results, busy=%v, %d calls", len(got), m.fac.busy, factoryCalls(t, dir))
	}
	if len(got[0].repos) != 2 || got[0].repos[0].name != "one" || got[0].repos[1].name != "two" && got[0].repos[1].name != "three" {
		t.Fatalf("repositories: %+v", got[0].repos)
	}
	if more := upd(m, press("1")); len(more) != 0 {
		t.Fatal("leaving starts nothing")
	}
	if more := upd(m, press("4")); len(only[factoryMsg](more)) != 0 || factoryCalls(t, dir) != 2 {
		t.Fatal("one refresh in flight at most")
	}
	upd(m, got[0])
	if m.fac.busy || len(m.fac.repos) != 2 || m.fac.doneAt.IsZero() || m.fac.cap != 5 {
		t.Fatalf("the result is applied: %+v", m.fac)
	}
}

func TestFactoryCadence(t *testing.T) {
	m, dir, now := factoryStubModel(t)
	upd(m, only[factoryMsg](upd(m, press("4")))[0])
	if got := only[factoryMsg](upd(m, tickMsg{})); len(got) != 0 {
		t.Fatal("a tick right after a result starts nothing")
	}
	*now = now.Add(29 * time.Second)
	if got := only[factoryMsg](upd(m, tickMsg{})); len(got) != 0 {
		t.Fatal("a tick before 30 s starts nothing")
	}
	*now = now.Add(2 * time.Second)
	got := only[factoryMsg](upd(m, tickMsg{}))
	if len(got) != 1 || factoryCalls(t, dir) != 4 {
		t.Fatalf("a tick after 30 s starts one refresh: %d, %d calls", len(got), factoryCalls(t, dir))
	}
	if more := only[factoryMsg](upd(m, tickMsg{})); len(more) != 0 {
		t.Fatal("a tick while one is in flight starts nothing")
	}
	upd(m, got[0])

	send(m, "1")
	*now = now.Add(time.Hour)
	if got := only[factoryMsg](upd(m, tickMsg{})); len(got) != 0 {
		t.Fatal("nothing runs while another view is shown")
	}
	if got := only[factoryMsg](upd(m, press("4"))); len(got) != 1 {
		t.Fatal("entering after a long time refreshes")
	}
}

func TestFactorySecondEntryWithinFiveSeconds(t *testing.T) {
	m, _, now := factoryStubModel(t)
	upd(m, only[factoryMsg](upd(m, press("4")))[0])
	send(m, "1")
	*now = now.Add(4 * time.Second)
	if got := only[factoryMsg](upd(m, press("4"))); len(got) != 0 {
		t.Fatal("a result younger than 5 s is kept")
	}
	send(m, "1")
	*now = now.Add(2 * time.Second)
	if got := only[factoryMsg](upd(m, press("4"))); len(got) != 1 {
		t.Fatal("an older one is refreshed")
	}
}

func TestFactoryResultForAChangedRepositoryListIsAppliedWhole(t *testing.T) {
	m, _, _ := factoryStubModel(t)
	upd(m, only[factoryMsg](upd(m, press("4")))[0])
	upd(m, factoryMsg{repos: []factoryRepo{{name: "other", path: "/x/other", st: fstatus(9)}}, cap: 5})
	if len(m.fac.repos) != 1 || m.fac.repos[0].name != "other" || m.fac.cap != 9 {
		t.Fatalf("applied whole: %+v", m.fac)
	}
}

func TestFactoryRangeChangeRefreshesWhenTheSessionsArrive(t *testing.T) {
	m, dir, _ := factoryStubModel(t)
	upd(m, only[factoryMsg](upd(m, press("4")))[0])
	before := factoryCalls(t, dir)
	res := &data.SessionsResult{}
	for _, s := range m.sessions {
		res.Sessions = append(res.Sessions, *s)
	}
	if got := only[factoryMsg](upd(m, sessionsMsg{gen: m.loadGen, res: res})); len(got) != 1 || factoryCalls(t, dir) != before+2 {
		t.Fatalf("a new session list refreshes the shown view: %d", len(got))
	}
}

func TestFactoryErrorsAreOneLinePerRepository(t *testing.T) {
	m, dir, _ := factoryStubModel(t)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	upd(m, only[factoryMsg](upd(m, press("4")))[0])
	for _, r := range m.fac.repos {
		if r.st != nil || r.err != "not inside a git repository" {
			t.Fatalf("%+v", r)
		}
	}
	if out := plain(m); strings.Count(out, "⚠ not inside a git repository") != 2 {
		t.Fatalf("each failed repository shows its error:\n%s", out)
	}
}

// ---- the cap field ----------------------------------------------------------------

func settingsFile(t *testing.T, content string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	path := data.FactorySettingsPath()
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func capModel(t *testing.T, content string) (*model, string) {
	path := settingsFile(t, content)
	m := factoryModel(true, 100, 30, alpha())
	return m, path
}

func TestCapFieldOpensWithTheFileValue(t *testing.T) {
	m, _ := capModel(t, `{"worktree_cap": 12}`)
	send(m, "c")
	if !m.fac.editing || m.fac.field != "12" {
		t.Fatalf("editing=%v field=%q", m.fac.editing, m.fac.field)
	}
	m, _ = capModel(t, "")
	send(m, "c")
	if m.fac.field != "5" {
		t.Fatalf("no file: the default, got %q", m.fac.field)
	}
	dash := fakeModel(true, 100, 30)
	send(dash, "c")
	if dash.fac.editing {
		t.Fatal("c does nothing outside the factory view")
	}
}

func TestCapFieldTyping(t *testing.T) {
	m, _ := capModel(t, "")
	send(m, "c", "backspace", "backspace")
	if m.fac.field != "" {
		t.Fatalf("backspace erases: %q", m.fac.field)
	}
	send(m, "1", "2", "3", "4", "x")
	if m.fac.field != "123" {
		t.Fatalf("three digits at most, digits only: %q", m.fac.field)
	}
	send(m, "backspace")
	if m.fac.field != "12" {
		t.Fatalf("%q", m.fac.field)
	}
	send(m, "up", "k")
	if m.fac.field != "14" {
		t.Fatalf("up and k add one: %q", m.fac.field)
	}
	send(m, "down", "j", "j")
	if m.fac.field != "11" {
		t.Fatalf("down and j subtract one: %q", m.fac.field)
	}
}

func TestCapFieldBounds(t *testing.T) {
	m, _ := capModel(t, `{"worktree_cap": 2}`)
	send(m, "c", "down", "down", "down")
	if m.fac.field != "1" {
		t.Fatalf("never below 1: %q", m.fac.field)
	}
	send(m, "backspace", "down")
	if m.fac.field != "1" {
		t.Fatalf("an empty field steps to 1: %q", m.fac.field)
	}
	send(m, "backspace", "9", "9", "9", "up")
	if m.fac.field != "999" {
		t.Fatalf("never above 999: %q", m.fac.field)
	}
}

func TestCapFieldRefusesInvalidAndKeepsOpen(t *testing.T) {
	m, path := capModel(t, "")
	send(m, "c", "backspace", "enter")
	if !m.fac.editing || m.fac.fieldErr == "" {
		t.Fatalf("an empty field stays open with a message: %+v", m.fac)
	}
	if !strings.Contains(plain(m), m.fac.fieldErr) {
		t.Fatal("the message is on screen")
	}
	send(m, "0", "enter")
	if !m.fac.editing || m.fac.fieldErr == "" {
		t.Fatal("0 stays open with a message")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("nothing was written")
	}
	send(m, "7")
	if m.fac.fieldErr != "" {
		t.Fatal("typing clears the message")
	}
}

func TestCapFieldEscLeavesTheFileUntouched(t *testing.T) {
	m, path := capModel(t, `{"worktree_cap": 3}`)
	send(m, "c", "9", "esc")
	if m.fac.editing {
		t.Fatal("esc closes the field")
	}
	if b, _ := os.ReadFile(path); string(b) != `{"worktree_cap": 3}` {
		t.Fatalf("file: %q", b)
	}
}

func TestCapFieldSwallowsOtherKeys(t *testing.T) {
	m, _ := capModel(t, "")
	send(m, "c")
	for _, k := range []string{"q", "tab", "4", "1", "?", "r", "s"} {
		if cmd := m.handleKey(press(k)); cmd != nil {
			t.Fatalf("%s did something while the field is open", k)
		}
		if m.view != viewFactory || !m.fac.editing || m.help {
			t.Fatalf("%s changed the screen: view=%v editing=%v help=%v", k, m.view, m.fac.editing, m.help)
		}
	}
	if cmd := m.handleKey(press("ctrl+c")); cmd == nil {
		t.Fatal("ctrl+c still quits")
	}
}

func TestCapFieldRefusesANonObjectFile(t *testing.T) {
	m, path := capModel(t, `[1, 2]`)
	send(m, "c")
	if m.fac.editing || !m.toast.err || !strings.Contains(m.toast.text, path) {
		t.Fatalf("a non-object file is reported by path and not opened: editing=%v toast=%+v", m.fac.editing, m.toast)
	}
	m.fac.editing, m.fac.field = true, "4"
	send(m, "enter")
	if !m.fac.editing || !strings.Contains(m.fac.fieldErr, path) {
		t.Fatalf("saving reports it too: %+v", m.fac)
	}
	if b, _ := os.ReadFile(path); string(b) != `[1, 2]` {
		t.Fatalf("the file was written: %q", b)
	}
}

func TestCapSaveWritesTheFileAndRefreshes(t *testing.T) {
	m, dir, _ := factoryStubModel(t)
	path := data.FactorySettingsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"lead_model": "fable", "worktree_cap": 3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	m.view = viewFactory
	send(m, "c", "backspace", "backspace", "8")
	got := only[factoryMsg](upd(m, press("enter")))
	b, _ := os.ReadFile(path)
	if string(b) != "{\n  \"lead_model\": \"fable\",\n  \"worktree_cap\": 8\n}\n" {
		t.Fatalf("file: %q", b)
	}
	if m.fac.editing || m.fac.cap != 8 || len(got) != 1 || factoryCalls(t, dir) != 2 || m.toast.text == "" {
		t.Fatalf("saved: editing=%v cap=%d refreshes=%d calls=%d toast=%q", m.fac.editing, m.fac.cap, len(got), factoryCalls(t, dir), m.toast.text)
	}
	if got[0].cap != 8 || got[0].capErr != nil {
		t.Fatalf("the refresh reads the new cap: %+v", got[0])
	}
}

func TestCapSaveDuringARefreshFollowsIt(t *testing.T) {
	m, dir, _ := factoryStubModel(t)
	first := only[factoryMsg](upd(m, press("4")))
	send(m, "c", "6")
	if got := only[factoryMsg](upd(m, press("enter"))); len(got) != 0 {
		t.Fatal("one refresh in flight at most")
	}
	if !m.fac.again {
		t.Fatal("the save asks for another refresh")
	}
	second := only[factoryMsg](upd(m, first[0]))
	if len(second) != 1 || m.fac.again || factoryCalls(t, dir) != 4 {
		t.Fatalf("a second refresh follows the first: %d, again=%v, calls=%d", len(second), m.fac.again, factoryCalls(t, dir))
	}
}

// ---- snapshot ---------------------------------------------------------------------

func TestSnapshotFactory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	t.Setenv("HOME", t.TempDir())
	repo := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	sessions := fmt.Sprintf(`{"sessions":[{"id":"s1","project":"proj","repo":"proj","repo_path":%q}],"malformed":0}`, repo)
	status, err := os.ReadFile("../data/testdata/factory-status.json")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(sessions), 0o644)
	os.WriteFile(filepath.Join(dir, "status.json"), status, 0o644)
	py, _ := filepath.Abs("../data/testdata/fakepy.sh")
	root, _ := filepath.Abs("../..")
	r := &data.Runner{Root: root, Python: py, ProjectsDir: dir, Timeout: 5 * time.Second}
	out, err := Snapshot(SnapshotOptions{Options: Options{Runner: r, NoColor: true, Range: "all", Now: func() time.Time { return goldenNow }}, View: "factory", W: 100, H: 30})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[4 Factory]", "proj", "stacked", "cost-in-money", "in motion ●○○○○○○ 1/7", "worktree cap  7"} {
		if !strings.Contains(out, want) {
			t.Errorf("snapshot lacks %q:\n%s", want, out)
		}
	}
}
