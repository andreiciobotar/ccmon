package data

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// factoryStub is a runner whose interpreter is the stub script; STUBDIR holds its switches.
func factoryStub(t *testing.T) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("STUBDIR", dir)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	py, err := filepath.Abs("testdata/fakepy.sh")
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{Root: root, Python: py, Timeout: 700 * time.Millisecond}, dir
}

func TestFactoryStatusDecodesNulls(t *testing.T) {
	r, dir := factoryStub(t)
	b, err := os.ReadFile("testdata/factory-status.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	st, err := r.FactoryStatus(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if st.Cap != 7 || st.InMotion != 1 || st.InReview != 1 || len(st.Rows) != 2 {
		t.Fatalf("status %+v", st)
	}
	if a := st.Rows[0]; a.Elapsed != nil || a.Slug != "cost-in-money" || a.PRState != "unknown" {
		t.Fatalf("row with nulls: %+v", a)
	}
	if b := st.Rows[1]; b.Elapsed == nil || *b.Elapsed != 252 || !b.Overtime || !b.Stale || b.PRURL == "" {
		t.Fatalf("row: %+v", b)
	}
	log, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if !strings.HasPrefix(string(log), "factory ") || !strings.HasSuffix(strings.TrimSpace(string(log)), repo[strings.Index(repo, "TestFactory"):]) {
		t.Fatalf("the command runs in the repository %s: %q", repo, log)
	}
}

func TestFactoryStatusErrors(t *testing.T) {
	r, dir := factoryStub(t)
	os.WriteFile(filepath.Join(dir, "fail"), nil, 0o644)
	if _, err := r.FactoryStatus(context.Background(), t.TempDir()); err == nil || err.Error() != "not inside a git repository" {
		t.Fatalf("exit 2 gives the stderr line without its prefix: %v", err)
	}
	os.Remove(filepath.Join(dir, "fail"))

	os.WriteFile(filepath.Join(dir, "status.json"), []byte("nope"), 0o644)
	if _, err := r.FactoryStatus(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "decode factory output") {
		t.Fatalf("decode error: %v", err)
	}
	os.Remove(filepath.Join(dir, "status.json"))

	missing := &Runner{Root: t.TempDir(), Python: r.Python}
	if _, err := missing.FactoryStatus(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "factory command not found: "+filepath.Join(missing.Root, "factory", "bin", "factory")) {
		t.Fatalf("missing script names its path: %v", err)
	}
}

func TestFactoryStatusTimesOutAndFreesTheRunner(t *testing.T) {
	r, dir := factoryStub(t)
	os.WriteFile(filepath.Join(dir, "hang"), nil, 0o644)
	start := time.Now()
	_, err := r.FactoryStatus(context.Background(), t.TempDir())
	if err == nil || err.Error() != "factory status timed out after 700ms" {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a hung call must be killed at its timeout")
	}
	done := make(chan struct{})
	go func() { r.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the call timed out")
	}
}

func TestFactoryStatusNoProcessAfterWait(t *testing.T) {
	r, dir := factoryStub(t)
	r.Wait()
	if _, err := r.FactoryStatus(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a call after Wait must fail")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "calls")); len(b) != 0 {
		t.Fatalf("a process started after Wait: %q", b)
	}
}

func TestFactoryStatusRealScriptInEmptyRepo(t *testing.T) {
	for _, tool := range []string{"git", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s missing", tool)
		}
	}
	root, _ := filepath.Abs("../..")
	repo := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	st, err := (&Runner{Root: root}).FactoryStatus(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Rows) != 0 || st.Cap != 5 {
		t.Fatalf("empty repository: %+v", st)
	}
}

func settings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "factory", "settings.json")
	if content != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestReadWorktreeCap(t *testing.T) {
	ok := map[string]int{"": 5, `{}`: 5, `{"lead_model":"x"}`: 5, `{"worktree_cap": 12}`: 12}
	for content, want := range ok {
		if got, err := ReadWorktreeCap(settings(t, content)); err != nil || got != want {
			t.Errorf("%q: %d, %v; want %d", content, got, err, want)
		}
	}
	for _, content := range []string{`{"worktree_cap":0}`, `{"worktree_cap":-1}`, `{"worktree_cap":1.5}`, `{"worktree_cap":2.0}`, `{"worktree_cap":true}`, `{"worktree_cap":"3"}`, `{"worktree_cap":null}`} {
		path := settings(t, content)
		if _, err := ReadWorktreeCap(path); err == nil || !strings.Contains(err.Error(), "worktree_cap") || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: want an error naming the key and the file, got %v", content, err)
		}
	}
	for _, content := range []string{`[1]`, `5`, `null`, `not json`, `{"a":`} {
		path := settings(t, content)
		if _, err := ReadWorktreeCap(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%q: want an error naming the file, got %v", content, err)
		}
	}
}

func TestWriteWorktreeCapCreatesDirectoryAndFile(t *testing.T) {
	path := settings(t, "")
	if err := WriteWorktreeCap(path, 8); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{\n  \"worktree_cap\": 8\n}\n" {
		t.Fatalf("file: %q", b)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Fatalf("a new file is 0644, got %v", st.Mode().Perm())
	}
	if got, err := ReadWorktreeCap(path); err != nil || got != 8 {
		t.Fatalf("round trip: %d, %v", got, err)
	}
}

func TestWriteWorktreeCapKeepsTheRest(t *testing.T) {
	nested := "{\"a\":  [1,\n 2], \"b\":null}"
	path := settings(t, "{\"z\": 2.0, \"lead_model\": \"fable\", \"nested\": "+nested+", \"worktree_cap\": 3}")
	if err := WriteWorktreeCap(path, 12); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	want := "{\n  \"lead_model\": \"fable\",\n  \"nested\": " + nested + ",\n  \"worktree_cap\": 12,\n  \"z\": 2.0\n}\n"
	if string(b) != want {
		t.Fatalf("got %q\nwant %q", b, want)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Fatalf("an existing file keeps its mode, got %v", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left behind: %v", entries)
	}
}

func TestWriteWorktreeCapRefusesANonObject(t *testing.T) {
	for _, content := range []string{`[1, 2]`, `null`, `oops`} {
		path := settings(t, content)
		err := WriteWorktreeCap(path, 4)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("%q: want an error naming the file, got %v", content, err)
		}
		if b, _ := os.ReadFile(path); string(b) != content {
			t.Fatalf("%q was overwritten: %q", content, b)
		}
		if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
			t.Fatalf("temporary file left: %v", entries)
		}
	}
}
