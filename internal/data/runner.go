package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrPython means python3 could not be found.
var ErrPython = errors.New("python3 not found on PATH")

// ErrPackage means the ccmon Python package could not be located.
var ErrPackage = errors.New("ccmon Python package not found")

// Runner shells out to `python3 -m ccmon`.
type Runner struct {
	Root        string // repo root (working directory, contains ccmon/__init__.py)
	Python      string // interpreter; default python3
	ProjectsDir string // passed through when non-empty
	AgentsDir   string // passed through when non-empty
	Err         error  // set when the root could not be located; reported by Check
}

// LocateRoot finds the repo root: $CCMON_ROOT, else walking up from the executable
// (bin/ccmon-tui -> ..), else walking up from the working directory.
func LocateRoot() (string, error) {
	if env := os.Getenv("CCMON_ROOT"); env != "" {
		if isRoot(env) {
			return env, nil
		}
		return env, fmt.Errorf("%w: CCMON_ROOT=%s has no ccmon/__init__.py", ErrPackage, env)
	}
	var starts []string
	if exe, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
		starts = append(starts, filepath.Dir(exe))
	}
	if wd, err := os.Getwd(); err == nil {
		starts = append(starts, wd)
	}
	for _, s := range starts {
		for d := s; ; d = filepath.Dir(d) {
			if isRoot(d) {
				return d, nil
			}
			if filepath.Dir(d) == d {
				break
			}
		}
	}
	return "", fmt.Errorf("%w: set CCMON_ROOT to the repo root (searched from %s)", ErrPackage, strings.Join(starts, ", "))
}

func isRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "ccmon", "__init__.py"))
	return err == nil
}

// Check verifies python3 and the package are usable.
func (r *Runner) Check() error {
	if r.Err != nil {
		return r.Err
	}
	if _, err := exec.LookPath(r.python()); err != nil {
		return ErrPython
	}
	if !isRoot(r.Root) {
		return fmt.Errorf("%w: %s", ErrPackage, r.Root)
	}
	return nil
}

func (r *Runner) python() string {
	if r.Python != "" {
		return r.Python
	}
	return "python3"
}

func (r *Runner) run(ctx context.Context, out any, args ...string) error {
	full := []string{"-m", "ccmon"}
	full = append(full, args...)
	full = append(full, "--json")
	if r.ProjectsDir != "" {
		full = append(full, "--projects-dir", r.ProjectsDir)
	}
	if r.AgentsDir != "" {
		full = append(full, "--agents-dir", r.AgentsDir)
	}
	cmd := exec.CommandContext(ctx, r.python(), full...)
	cmd.Dir = r.Root
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrPython
		}
		msg := strings.TrimSpace(se.String())
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "No module named ccmon") {
			return fmt.Errorf("%w: %s", ErrPackage, r.Root)
		}
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return errors.New(msg)
	}
	if err := json.Unmarshal(so.Bytes(), out); err != nil {
		return fmt.Errorf("decode ccmon output: %w", err)
	}
	return nil
}

func sinceArgs(since string) []string {
	if since == "" || since == "all" {
		return nil
	}
	return []string{"--since", since}
}

// Sessions runs `ccmon sessions [--since X] --json`.
func (r *Runner) Sessions(ctx context.Context, since string) (*SessionsResult, error) {
	var out SessionsResult
	args := append([]string{"sessions"}, sinceArgs(since)...)
	if err := r.run(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// Session runs `ccmon session <id> --json`.
func (r *Runner) Session(ctx context.Context, id string) (*Session, error) {
	var out Session
	if err := r.run(ctx, &out, "session", id); err != nil {
		return nil, err
	}
	out.Detail = true
	return &out, nil
}

// Agents runs `ccmon agents [--since X] --json`.
func (r *Runner) Agents(ctx context.Context, since string) (*Agents, error) {
	var out Agents
	args := append([]string{"agents"}, sinceArgs(since)...)
	if err := r.run(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// StatMtime returns the transcript mtime of a session path, zero when missing.
func StatMtime(path string) (t int64) {
	if fi, err := os.Stat(path); err == nil {
		return fi.ModTime().UnixNano()
	}
	return 0
}
