package data

import (
	"context"
	"os"
	"path/filepath"
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
	if len(s.Dispatches) != 18 || !s.Detail {
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

func TestSessionIDFor(t *testing.T) {
	root := "/p"
	cases := map[string]string{
		"/p/proj/abc.jsonl":                       "abc",
		"/p/proj/abc/subagents/agent-1.jsonl":     "abc",
		"/p/proj/abc/subagents/agent-1.meta.json": "",
		"/p/proj/abc/memory/x.jsonl":              "",
		"/q/proj/abc.jsonl":                       "",
	}
	for path, want := range cases {
		got, ok := SessionIDFor(root, path)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q,%v want %q", path, got, ok, want)
		}
	}
}

func TestWatchEmitsOnAppend(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "sess1.jsonl")
	if err := os.WriteFile(f, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := Watch(ctx, root, 200*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	fh, _ := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0o644)
	fh.WriteString("{}\n")
	fh.Close()
	select {
	case c := <-ch:
		if c.SessionID != "sess1" {
			t.Fatalf("got %+v", c)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no change event")
	}
	// a brand new session directory with a subagent file
	sub := filepath.Join(dir, "sess2", "subagents")
	os.MkdirAll(sub, 0o755)
	time.Sleep(50 * time.Millisecond)
	os.WriteFile(filepath.Join(sub, "agent-x.jsonl"), []byte("{}\n"), 0o644)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case c := <-ch:
			if c.SessionID == "sess2" {
				return
			}
		case <-deadline:
			t.Fatal("no event for new subagent dir")
		}
	}
}
