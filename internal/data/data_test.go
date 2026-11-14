package data

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureRunner(t *testing.T) *Runner {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		Root:        root,
		ProjectsDir: filepath.Join(root, "tests", "fixtures", "projects"),
		AgentsDir:   filepath.Join(root, "tests", "fixtures", "agents"),
	}
	if err := r.Check(); err != nil {
		t.Skipf("python unavailable: %v", err)
	}
	return r
}

func TestSessionsFixture(t *testing.T) {
	r := fixtureRunner(t)
	res, err := r.Sessions(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sessions) != 1 || res.Malformed != 2 {
		t.Fatalf("sessions=%d malformed=%d", len(res.Sessions), res.Malformed)
	}
	s := res.Sessions[0]
	if s.DispatchCount != 18 || s.Repeats != 1 || s.ReviewLoops != 3 || s.WarnCount != 1 || s.Malformed != 2 {
		t.Fatalf("unexpected signals: %+v", s)
	}
	if s.ByAgent["main"].Total != 300 {
		t.Fatalf("main total = %d", s.ByAgent["main"].Total)
	}
	if s.Tokens.Total != s.Tokens.Main+s.Tokens.Subagents {
		t.Fatal("token split does not add up")
	}
	if s.Start == nil || s.End == nil {
		t.Fatal("missing span")
	}
}

func TestSessionDetailFixture(t *testing.T) {
	r := fixtureRunner(t)
	s, err := r.Session(context.Background(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Dispatches) != 18 {
		t.Fatalf("dispatches=%d", len(s.Dispatches))
	}
	var found bool
	for _, d := range s.Dispatches {
		if d.Description == "Find config loader again" {
			found = true
			if d.Status != "running" || len(d.Flags) != 1 || d.Flags[0] != "repeat" || d.RepeatOf == nil || *d.RepeatOf != "toolu_E1" {
				t.Fatalf("bad dispatch %+v", d)
			}
			if _, ok := d.Duration(time.Now()); !ok {
				t.Fatal("running dispatch should estimate a duration when start is known")
			}
		}
	}
	if !found {
		t.Fatal("dispatch not found")
	}
	if len(s.ReviewLoopEvent) != 3 || s.ReviewLoopEvent[2].Via != "resume" {
		t.Fatalf("review loop events: %+v", s.ReviewLoopEvent)
	}
}

func TestAgentsFixture(t *testing.T) {
	r := fixtureRunner(t)
	a, err := r.Agents(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Agents[0].Agent != "main" {
		t.Fatalf("first agent %q", a.Agents[0].Agent)
	}
	for _, row := range a.Agents {
		if row.Agent == "explorer" && (row.Dispatches != 10 || row.Usage.Output != 39 || row.WarnCount != 1) {
			t.Fatalf("explorer row %+v", row)
		}
		if row.Agent == "coder" && row.ReviewLoops != 3 {
			t.Fatalf("coder row %+v", row)
		}
	}
	if _, ok := a.ByModel["claude-haiku-5-5"]; !ok {
		t.Fatal("missing haiku model")
	}
}

func TestSessionError(t *testing.T) {
	r := fixtureRunner(t)
	if _, err := r.Session(context.Background(), "nope-nope"); err == nil {
		t.Fatal("expected error for unknown session")
	}
}

func TestMissingPython(t *testing.T) {
	r := &Runner{Root: "../..", Python: "definitely-not-python-xyz"}
	if err := r.Check(); err != ErrPython {
		t.Fatalf("got %v", err)
	}
	if _, err := r.Sessions(context.Background(), ""); err != ErrPython {
		t.Fatalf("got %v", err)
	}
}

func TestMissingPackage(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	if err := r.Check(); err == nil {
		t.Fatal("expected package error")
	}
}

func writeTranscript(t *testing.T, path string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	writeTranscript(t, filepath.Join(root, "proj", "s1.jsonl"), t1)
	writeTranscript(t, filepath.Join(root, "proj", "s2.jsonl"), t1)
	writeTranscript(t, filepath.Join(root, "proj", "s2", "subagents", "agent-1.jsonl"), t2) // bumps s2
	writeTranscript(t, filepath.Join(root, "proj", "s2", "subagents", "agent-1.meta.json"), t2.Add(time.Hour))
	writeTranscript(t, filepath.Join(root, "proj", "orphan", "subagents", "agent-1.jsonl"), t2) // no main transcript
	writeTranscript(t, filepath.Join(root, "proj", "s3", "memory", "x.jsonl"), t2)              // not a subagent file
	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got["s1"].Equal(t1) || !got["s2"].Equal(t2) {
		t.Fatalf("scan = %v", got)
	}
	if err := os.Remove(filepath.Join(root, "proj", "s1.jsonl")); err != nil {
		t.Fatal(err)
	}
	if got, _ = Scan(root); len(got) != 1 {
		t.Fatalf("a deleted transcript must disappear, got %v", got)
	}
	if _, err := Scan(filepath.Join(root, "missing")); err == nil {
		t.Fatal("an unreadable root is an error, not an empty corpus")
	}
}

func TestScanSkipsUnreadableProject(t *testing.T) {
	root := t.TempDir()
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	writeTranscript(t, filepath.Join(root, "good", "s1.jsonl"), t1)
	writeTranscript(t, filepath.Join(root, "locked", "s2.jsonl"), t1)
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	if _, err := os.ReadDir(locked); err == nil {
		t.Skip("directory permissions are not enforced (running as root?)")
	}
	got, err := Scan(root)
	if err != nil || len(got) != 1 || !got["s1"].Equal(t1) {
		t.Fatalf("an unreadable project is skipped: %v, %v", got, err)
	}
}

func stubRunner(t *testing.T) *Runner {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	py, _ := filepath.Abs("testdata/fakepy.sh")
	return &Runner{Root: "../..", Python: py, ProjectsDir: dir, Timeout: 500 * time.Millisecond}
}

func TestCallTimeout(t *testing.T) {
	r := stubRunner(t)
	os.WriteFile(filepath.Join(os.Getenv("STUBDIR"), "hang"), nil, 0o644)
	start := time.Now()
	_, err := r.Session(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the hung call was not killed")
	}
}

func TestCancelKillsCallAndWaitReturns(t *testing.T) {
	r := stubRunner(t)
	r.Timeout = time.Minute
	os.WriteFile(filepath.Join(os.Getenv("STUBDIR"), "hang"), nil, 0o644)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := r.Agents(ctx, ""); done <- err }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	waited := make(chan struct{})
	go func() { r.Wait(); close(waited) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the call")
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after cancel")
	}
}

func TestCallAfterWaitDoesNotStart(t *testing.T) {
	r := stubRunner(t)
	r.Wait()
	if _, err := r.Session(context.Background(), "x"); err == nil {
		t.Fatal("a call after Wait must fail")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("STUBDIR"), "calls")); err == nil {
		t.Fatal("no process may start after Wait")
	}
}

func TestSessionsGroupingFields(t *testing.T) {
	r := fixtureRunner(t)
	r.ProjectsDir = filepath.Join(r.Root, "tests", "fixtures", "grouping")
	res, err := r.Sessions(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]*Session{}
	for i := range res.Sessions {
		got[res.Sessions[i].ID[:8]] = &res.Sessions[i]
	}
	for id, want := range map[string][3]string{
		"a1000001": {"alpha", "/r/alpha", ""},
		"a1000002": {"alpha", "/r/alpha", "ci"},
		"c1000001": {"-no-cwd", "", ""}, // a null repo_path decodes to the empty string
	} {
		s := got[id]
		if s == nil {
			t.Fatalf("missing session %s", id)
		}
		if s.Repo != want[0] || s.RepoPath != want[1] || s.Task != want[2] {
			t.Errorf("%s: repo %q path %q task %q, want %q", id, s.Repo, s.RepoPath, s.Task, want)
		}
	}
	if l := got["a1000002"].Label(); l != "alpha/ci" {
		t.Errorf("label %q", l)
	}
	if l := got["a1000001"].Label(); l != "alpha" {
		t.Errorf("label %q", l)
	}
}
