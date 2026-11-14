package data

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrPython means python3 could not be found.
var ErrPython = errors.New("python3 not found on PATH")

// ErrPackage means the isshoni Python package could not be located.
var ErrPackage = errors.New("isshoni Python package not found")

// callTimeout bounds one python call when Runner.Timeout is unset.
const callTimeout = 2 * time.Minute

// Runner shells out to `python3 -m isshoni`.
type Runner struct {
	Root        string        // repo root (working directory, contains isshoni/__init__.py)
	Python      string        // interpreter; default python3
	ProjectsDir string        // absolute; passed through when non-empty
	AgentsDir   string        // absolute; passed through when non-empty
	Timeout     time.Duration // per call; default callTimeout
	Err         error         // set when the root could not be located; reported by Check

	mu     sync.Mutex
	closed bool // set by Wait: no call starts after it
	calls  sync.WaitGroup
}

// Projects is the transcripts root the CLI reads.
func (r *Runner) Projects() string {
	if r.ProjectsDir != "" {
		return r.ProjectsDir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Wait blocks until every call has returned; cancel their context first. Calls
// requested afterwards fail without starting a process.
func (r *Runner) Wait() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.calls.Wait()
}

// begin registers a call; false once Wait has been called.
func (r *Runner) begin() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.calls.Add(1)
	return true
}

// LocateRoot finds the repo root: $ISSHONI_ROOT, else walking up from the executable
// (bin/isshoni-tui -> ..), else walking up from the working directory.
func LocateRoot() (string, error) {
	if env := os.Getenv("ISSHONI_ROOT"); env != "" {
		if isRoot(env) {
			return env, nil
		}
		return env, fmt.Errorf("%w: ISSHONI_ROOT=%s has no isshoni/__init__.py", ErrPackage, env)
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
	return "", fmt.Errorf("%w: set ISSHONI_ROOT to the repo root (searched from %s)", ErrPackage, strings.Join(starts, ", "))
}

func isRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "isshoni", "__init__.py"))
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
	if !r.begin() {
		return cmp.Or(ctx.Err(), context.Canceled)
	}
	defer r.calls.Done()
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = callTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := []string{"-m", "isshoni"}
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
	cmd.WaitDelay = time.Second
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return ErrPython
		}
		msg := strings.TrimSpace(se.String())
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("isshoni %s timed out after %s", args[0], timeout)
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		if msg == "" {
			msg = err.Error()
		}
		if strings.Contains(msg, "No module named isshoni") {
			return fmt.Errorf("%w: %s", ErrPackage, r.Root)
		}
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return errors.New(msg)
	}
	if err := json.Unmarshal(so.Bytes(), out); err != nil {
		return fmt.Errorf("decode isshoni output: %w", err)
	}
	return nil
}

func sinceArgs(since string) []string {
	if since == "" || since == "all" {
		return nil
	}
	return []string{"--since", since}
}

// Sessions runs `isshoni sessions [--since X] --json`.
func (r *Runner) Sessions(ctx context.Context, since string) (*SessionsResult, error) {
	var out SessionsResult
	args := append([]string{"sessions"}, sinceArgs(since)...)
	if err := r.run(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}

// Session runs `isshoni session <id> --json`.
func (r *Runner) Session(ctx context.Context, id string) (*Session, error) {
	var out Session
	if err := r.run(ctx, &out, "session", id); err != nil {
		return nil, err
	}
	return &out, nil
}

// Agents runs `isshoni agents [--since X] --json`.
func (r *Runner) Agents(ctx context.Context, since string) (*Agents, error) {
	var out Agents
	args := append([]string{"agents"}, sinceArgs(since)...)
	if err := r.run(ctx, &out, args...); err != nil {
		return nil, err
	}
	return &out, nil
}
