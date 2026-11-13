package tui

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"ccmon/internal/data"
)

// stubModel is a fake model whose runner is the stub in internal/data/testdata: python
// calls are logged to the returned directory, which also holds the fail/hang switches.
func stubModel(t *testing.T) (*model, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	py, err := filepath.Abs("../data/testdata/fakepy.sh")
	if err != nil {
		t.Fatal(err)
	}
	r := &data.Runner{Root: "../..", Python: py, ProjectsDir: dir, Timeout: 700 * time.Millisecond}
	m := newFake(Options{NoColor: true, Runner: r}, 100, 30)
	if m.fatal != nil {
		t.Skipf("stub runner unusable: %v", m.fatal)
	}
	return m, dir
}

// calls counts the python calls the stub saw whose log line starts with prefix.
func calls(t *testing.T, dir, prefix string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, "calls"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

// run executes a command, and the commands of a batch, returning the messages.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

func upd(m *model, msg tea.Msg) []tea.Msg {
	_, cmd := m.Update(msg)
	return run(cmd)
}

// only keeps the messages of one type.
func only[T any](msgs []tea.Msg) []T {
	var out []T
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

const sidA, sidB = "aaaaaaaa-0001", "bbbbbbbb-0002"

// changed is a poll in which the given session's transcript moved to t.
func changed(m *model, id string, t time.Time) scanMsg {
	seen := map[string]time.Time{}
	for k, v := range m.seen {
		seen[k] = v
	}
	seen[id] = t
	return scanMsg{seen: seen}
}

func TestChangeRefreshResultSequence(t *testing.T) {
	m, dir := stubModel(t)
	at := t0.Add(2 * time.Hour)

	d := only[detailMsg](upd(m, changed(m, sidA, at)))
	if len(d) != 1 || !m.inflight[sidA] || calls(t, dir, "session "+sidA) != 1 {
		t.Fatalf("first change starts one fetch: %d msgs, inflight=%v", len(d), m.inflight)
	}
	// more changes while it runs start nothing, however often they arrive
	for i := 1; i <= 3; i++ {
		if got := upd(m, changed(m, sidA, at.Add(time.Duration(i)*time.Second))); len(got) != 0 {
			t.Fatalf("change %d during a refresh launched %d commands", i, len(got))
		}
	}
	if !m.dirty[sidA] {
		t.Fatal("a change during the refresh marks the session dirty")
	}
	// its result triggers exactly one more
	d2 := only[detailMsg](upd(m, d[0]))
	if len(d2) != 1 || !m.inflight[sidA] || m.dirty[sidA] {
		t.Fatalf("one more refresh expected: %d msgs inflight=%v dirty=%v", len(d2), m.inflight, m.dirty)
	}
	if got := upd(m, d2[0]); len(got) != 0 || len(m.inflight) != 0 {
		t.Fatalf("nothing more to do, inflight=%v", m.inflight)
	}
	if n := calls(t, dir, "session "+sidA); n != 2 {
		t.Fatalf("%d fetches, want 2", n)
	}
	// each fetch is stamped with the mtime known when it started
	if !d[0].at.Equal(at) || !d2[0].at.Equal(at.Add(3*time.Second)) {
		t.Fatalf("fetches stamped %v and %v, want %v and %v", d[0].at, d2[0].at, at, at.Add(3*time.Second))
	}
	if got := m.details[sidA].LastChange; !got.Equal(at.Add(3 * time.Second)) {
		t.Fatalf("the newest result carries the mtime it was read at, got %v", got)
	}
}

func TestFailedRefreshFreesTheSession(t *testing.T) {
	m, dir := stubModel(t)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	d := only[detailMsg](upd(m, changed(m, sidA, t0.Add(2*time.Hour))))
	if len(d) != 1 || d[0].err == nil {
		t.Fatalf("expected a failing fetch, got %+v", d)
	}
	if got := upd(m, d[0]); len(got) != 0 || m.inflight[sidA] || m.dirty[sidA] {
		t.Fatalf("an error must neither relaunch nor keep the session in flight: %v %v", m.inflight, m.dirty)
	}
	if !m.toast.err {
		t.Fatal("the failure is shown")
	}
	os.Remove(filepath.Join(dir, "fail"))
	if len(only[detailMsg](upd(m, changed(m, sidA, t0.Add(3*time.Hour))))) != 1 {
		t.Fatal("the next change refreshes again")
	}
}

func TestHungCallTimesOutAndFrees(t *testing.T) {
	m, dir := stubModel(t)
	os.WriteFile(filepath.Join(dir, "hang"), nil, 0o644)
	start := time.Now()
	d := only[detailMsg](upd(m, changed(m, sidA, t0.Add(2*time.Hour))))
	if len(d) != 1 || d[0].err == nil || !strings.Contains(d[0].err.Error(), "timed out") {
		t.Fatalf("a hung call must come back as a timeout: %+v", d)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the timeout did not cut the call short")
	}
	upd(m, d[0])
	if m.inflight[sidA] {
		t.Fatal("a timed-out call must not hold the session in flight")
	}
}

func TestFetchInterleavedWithFollowAndEnter(t *testing.T) {
	m, dir := stubModel(t)
	// b has no detail yet; a change puts a refresh in flight
	d := only[detailMsg](upd(m, changed(m, sidB, t0.Add(2*time.Hour))))
	m.selID = sidB
	// enter and follow both want b's detail: neither starts a second fetch
	if got := upd(m, press("enter")); len(got) != 0 {
		t.Fatalf("enter during a refresh launched %d commands", len(got))
	}
	m.view = viewDashboard
	if got := upd(m, press("f")); len(got) != 0 {
		t.Fatalf("follow during a refresh launched %d commands", len(got))
	}
	if calls(t, dir, "session "+sidB) != 1 {
		t.Fatal("exactly one fetch for the session")
	}
	upd(m, d[0])
	if m.details[sidB] == nil || len(m.inflight) != 0 {
		t.Fatal("the one result serves the refresh, the open view and follow")
	}

	// and the other way round: enter fetches, a change during it queues one more
	m2, dir2 := stubModel(t)
	m2.selID = sidB
	e := only[detailMsg](upd(m2, press("enter")))
	if len(e) != 1 {
		t.Fatalf("enter fetches the detail once, got %d", len(e))
	}
	upd(m2, changed(m2, sidB, t0.Add(3*time.Hour)))
	again := only[detailMsg](upd(m2, e[0]))
	if len(again) != 1 || calls(t, dir2, "session "+sidB) != 2 {
		t.Fatalf("the change that arrived meanwhile triggers exactly one more fetch")
	}
}

func TestFailedAgentsRefreshWaitsForNextCadence(t *testing.T) {
	m, dir := stubModel(t)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	m.scanning = true // keep the poll out of this
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	m.agentsDirty, m.agentsAt = true, now.Add(-agentsEvery)

	tick := func() []agentsMsg {
		return only[agentsMsg](upd(m, tickMsg{gen: m.tickGen}))
	}
	first := tick()
	if len(first) != 1 || first[0].err == nil {
		t.Fatalf("the due refresh runs and fails: %+v", first)
	}
	upd(m, first[0])
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		if got := tick(); len(got) != 0 {
			t.Fatalf("tick %d relaunched a failed agents refresh", i)
		}
	}
	now = now.Add(agentsEvery)
	if got := tick(); len(got) != 1 {
		t.Fatal("the next cadence tries again")
	}
	if n := calls(t, dir, "agents"); n != 2 {
		t.Fatalf("%d agents calls, want 2", n)
	}
}

func TestSlowFullLoadKeepsNewerSessionResult(t *testing.T) {
	m, _ := stubModel(t)
	cur := m.sessions[sidA]
	old := *cur
	old.LastChange = cur.LastChange.Add(-time.Hour)
	old.Tokens.Total = 1
	late := fakeSession("eeeeeeee-0005", "late", 0, 5, 5) // appeared after the load looked
	late.LastChange = t0.Add(5 * time.Hour)
	m.sessions[late.ID] = late
	m.seen[late.ID] = late.LastChange
	loadSeen := map[string]time.Time{sidA: old.LastChange, sidB: m.seen[sidB], "cccccccc-0003": m.seen["cccccccc-0003"]}
	upd(m, sessionsMsg{gen: m.loadGen, seen: loadSeen, res: &data.SessionsResult{Sessions: []data.Session{old, *m.sessions[sidB]}}})
	if got := m.sessions[sidA]; got.Tokens.Total != cur.Tokens.Total {
		t.Fatalf("a full load that read older data overwrote a newer session: total %d", got.Tokens.Total)
	}
	if m.sessions[late.ID] == nil {
		t.Fatal("a session that appeared while the load ran was dropped")
	}
	if m.sessions["cccccccc-0003"] != nil {
		t.Fatal("a session outside the loaded range and unchanged since must be dropped")
	}
}

func TestPollDropsVanishedSessions(t *testing.T) {
	m, _ := stubModel(t)
	m.selID, m.view = sidA, viewSession
	seen := map[string]time.Time{sidB: m.seen[sidB], "cccccccc-0003": m.seen["cccccccc-0003"]}
	upd(m, scanMsg{seen: seen})
	if m.sessions[sidA] != nil || m.details[sidA] != nil {
		t.Fatal("a session whose transcript is gone must leave the list and the cache")
	}
	if m.view != viewDashboard || m.selID == sidA {
		t.Fatalf("the open session vanished: view=%v sel=%s", m.view, m.selID)
	}
	// a result that was in flight for it is ignored
	m.inflight[sidA] = true
	upd(m, detailMsg{id: sidA, res: fakeDetail(fakeSession(sidA, "alpha", 0, 1, 1))})
	if m.sessions[sidA] != nil {
		t.Fatal("a late result resurrected a deleted session")
	}
	// a failed scan is not evidence of anything
	upd(m, scanMsg{err: os.ErrPermission})
	if len(m.sessions) != 2 {
		t.Fatal("a failed poll must change nothing")
	}
}

func TestNewLoadCancelsTheOneItReplaces(t *testing.T) {
	m, _ := stubModel(t)
	first := m.loadCtx
	m.reload()
	if first.Err() == nil {
		t.Fatal("starting a load must cancel the previous one")
	}
	if m.loadCtx.Err() != nil {
		t.Fatal("the new load runs")
	}
	gen := m.loadGen
	m.reload()
	if m.loadGen != gen+1 {
		t.Fatal("each load has its own generation")
	}
}

// TestRunQuitCancelsCallsAndWaits drives the real Run loop: quitting must cancel the
// python call in flight and return once it has ended, long before its own timeout.
func TestRunQuitCancelsCallsAndWaits(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	os.WriteFile(filepath.Join(dir, "hang"), nil, 0o644)
	py, _ := filepath.Abs("../data/testdata/fakepy.sh")
	r := &data.Runner{Root: "../..", Python: py, ProjectsDir: dir, Timeout: time.Minute}
	if r.Check() != nil {
		t.Skip("stub runner unusable")
	}
	in, w := io.Pipe()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- runProgram(Options{Runner: r, NoColor: true}, tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithWindowSize(100, 30))
	}()
	for calls(t, dir, "sessions") == 0 {
		if time.Since(start) > 5*time.Second {
			t.Fatal("the load never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	w.Write([]byte("q"))
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after quit")
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("quit waited for the call's timeout")
	}
	if _, err := r.Session(context.Background(), "x"); err == nil {
		t.Fatal("Run must leave the runner closed: no call may start after quit")
	}
}

func TestRetryOnErrorScreenKeepsRange(t *testing.T) {
	m, _ := stubModel(t)
	m.sessions, m.details = map[string]*data.Session{}, map[string]*data.Session{}
	m.loading, m.refreshErr = false, "ccmon: boom"
	rng, gen := m.rangeIdx, m.loadGen
	if cmd := m.handleKey(press("r")); cmd == nil {
		t.Fatal("r retries")
	}
	if m.rangeIdx != rng || m.loadGen != gen+1 || !m.loading || m.refreshErr != "" {
		t.Fatalf("retry must reload the same range: range %d->%d gen %d->%d loading=%v err=%q", rng, m.rangeIdx, gen, m.loadGen, m.loading, m.refreshErr)
	}
}

func TestIdleTickIsSupersededWhenAnimationStarts(t *testing.T) {
	now := t0
	m := New(Options{Now: func() time.Time { return now }})
	m.loading = false
	if m.ensureTick() == nil || m.tickD != tickIdle {
		t.Fatal("an idle model ticks slowly")
	}
	idleGen := m.tickGen
	if m.ensureTick() != nil {
		t.Fatal("one pending tick is enough")
	}
	m.counter["tokens"].Set(1000)
	if m.ensureTick() == nil || m.tickD != tickFast || m.tickGen == idleGen {
		t.Fatal("an animation that starts during idle must not wait for the idle tick")
	}
	before := m.counter["tokens"].Cur
	m.Update(tickMsg{gen: idleGen})
	if m.counter["tokens"].Cur != before {
		t.Fatal("the superseded tick must be ignored")
	}
	// the fast tick advances by the time that really passed
	now = now.Add(300 * time.Millisecond)
	m.Update(tickMsg{gen: m.tickGen})
	var want Counter
	want.Set(1000)
	want.Step(300 * time.Millisecond)
	if m.counter["tokens"].Cur != want.Cur {
		t.Fatalf("tween advanced %v, want %v for 300ms", m.counter["tokens"].Cur, want.Cur)
	}
}

// ---- robust poll ---------------------------------------------------------------------

// pollModel is a model over a real projects directory: a readable project, one that
// cannot be read, and a runner that answers with the stub.
func pollModel(t *testing.T) (*model, string, string) {
	t.Helper()
	m, _ := stubModel(t)
	root := t.TempDir()
	m.runner.ProjectsDir = root
	good, locked := filepath.Join(root, "good"), filepath.Join(root, "locked")
	for _, p := range []string{good, locked} {
		os.MkdirAll(p, 0o755)
		os.WriteFile(filepath.Join(p, filepath.Base(p)+"-s.jsonl"), []byte("{}\n"), 0o644)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755); os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("directory permissions are not enforced")
	}
	m.sessions, m.details, m.seen, m.loaded = map[string]*data.Session{}, map[string]*data.Session{}, nil, false
	return m, root, locked
}

func TestUnreadableProjectDoesNotStopTheLoadOrThePoll(t *testing.T) {
	m, root, _ := pollModel(t)
	var load sessionsMsg
	for _, msg := range run(m.loadSessionsCmd()) {
		load = msg.(sessionsMsg)
	}
	if load.scanErr != nil || load.seen["good-s"].IsZero() || len(load.seen) != 1 {
		t.Fatalf("the readable project is scanned, the locked one skipped: %v err=%v", load.seen, load.scanErr)
	}
	upd(m, load)
	if !m.loaded || m.seen == nil || m.scanErr != "" {
		t.Fatalf("first load done: loaded=%v seen=%v scanErr=%q", m.loaded, m.seen, m.scanErr)
	}
	// polling runs, and a change to the readable project is noticed
	m.sessions[sidA] = fakeSession(sidA, "alpha", 0, 1, 1)
	m.polledAt = time.Time{}
	got := only[scanMsg](upd(m, tickMsg{gen: m.tickGen}))
	if len(got) != 1 || got[0].err != nil {
		t.Fatalf("the poll must start after the first load: %+v", got)
	}
	upd(m, got[0])
	// the root itself becoming unreadable is an error and marks the screen stale
	os.Chmod(root, 0)
	m.polledAt = time.Time{}
	got = only[scanMsg](upd(m, tickMsg{gen: m.tickGen}))
	if len(got) != 1 || got[0].err == nil {
		t.Fatalf("an unreadable root fails the scan: %+v", got)
	}
	upd(m, got[0])
	if m.scanErr == "" || !strings.Contains(plain(m), "stale") {
		t.Fatalf("a failing poll must mark the screen stale (scanErr=%q)", m.scanErr)
	}
	m.polledAt = time.Time{}
	got = only[scanMsg](upd(m, tickMsg{gen: m.tickGen}))
	if len(got) != 1 {
		t.Fatal("a failing poll keeps retrying")
	}
	upd(m, got[0])
	// and it recovers
	os.Chmod(root, 0o755)
	m.polledAt = time.Time{}
	upd(m, only[scanMsg](upd(m, tickMsg{gen: m.tickGen}))[0])
	if m.scanErr != "" || strings.Contains(plain(m), "stale") {
		t.Fatal("a scan that works again clears the stale marker")
	}
}

func TestPollStartsAfterAFailedFirstLoad(t *testing.T) {
	m, _, _ := pollModel(t)
	os.WriteFile(filepath.Join(os.Getenv("STUBDIR"), "fail"), nil, 0o644)
	var load sessionsMsg
	for _, msg := range run(m.loadSessionsCmd()) {
		load = msg.(sessionsMsg)
	}
	if load.err == nil {
		t.Fatal("the load fails")
	}
	upd(m, load)
	if !m.loaded || m.seen == nil {
		t.Fatalf("a failed first load still starts the poll: loaded=%v seen=%v", m.loaded, m.seen)
	}
	m.polledAt = time.Time{}
	if len(only[scanMsg](upd(m, tickMsg{gen: m.tickGen}))) != 1 {
		t.Fatal("the poll runs after a failed first load")
	}
}

func TestFailedSessionRefreshIsRetriedAtTheSlowCadence(t *testing.T) {
	m, dir := stubModel(t)
	m.scanning = true // keep the poll out of this
	now := t0.Add(time.Hour)
	m.now = func() time.Time { return now }
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	d := only[detailMsg](upd(m, changed(m, sidA, t0.Add(2*time.Hour))))
	if len(d) != 1 || d[0].err == nil {
		t.Fatalf("the refresh fails: %+v", d)
	}
	upd(m, d[0])
	tick := func() []detailMsg { return only[detailMsg](upd(m, tickMsg{gen: m.tickGen})) }
	for i := 0; i < 5; i++ { // the transcript stays unchanged: nothing retries before the cadence
		now = now.Add(time.Second)
		if got := tick(); len(got) != 0 {
			t.Fatalf("tick %d retried early", i)
		}
	}
	now = now.Add(agentsEvery)
	again := tick()
	if len(again) != 1 || again[0].err == nil {
		t.Fatalf("the due retry runs (and fails again here): %+v", again)
	}
	upd(m, again[0])
	os.Remove(filepath.Join(dir, "fail"))
	now = now.Add(agentsEvery)
	ok := tick()
	if len(ok) != 1 || ok[0].err != nil || !ok[0].at.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("the retry succeeds with the mtime of the unchanged transcript: %+v", ok)
	}
	upd(m, ok[0])
	if len(m.retry) != 0 || !m.details[sidA].LastChange.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("a success ends the retries: %v", m.retry)
	}
	now = now.Add(agentsEvery)
	if got := tick(); len(got) != 0 {
		t.Fatal("nothing left to retry")
	}
}
