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
	"sort"
	"strconv"
	"strings"
	"time"
)

// factoryTimeout bounds one `factory status` call when Runner.Timeout is unset.
const factoryTimeout = 30 * time.Second

// defaultWorktreeCap is the factory's default (load_settings in factory/bin/factory).
const defaultWorktreeCap = 5

// FactoryTask is one row of `factory status --json`.
type FactoryTask struct {
	Slug     string `json:"slug"`
	Phase    string `json:"phase"`
	Base     string `json:"base"`
	Elapsed  *int64 `json:"elapsed"` // seconds since the session started
	Overtime bool   `json:"overtime"`
	Stale    bool   `json:"stale"`
	PRState  string `json:"pr_state"`
	PRURL    string `json:"pr_url"`
	Session  string `json:"session"`
}

// FactoryStatus is what `factory status --json` prints for one repository.
type FactoryStatus struct {
	Rows     []FactoryTask `json:"rows"`
	Cap      int           `json:"cap"`
	InMotion int           `json:"in_motion"`
	InReview int           `json:"in_review"`
	Merged   int           `json:"merged"`
}

// FactoryStatus runs `factory status --json` in the repository at repoPath.
func (r *Runner) FactoryStatus(ctx context.Context, repoPath string) (*FactoryStatus, error) {
	script, err := filepath.Abs(filepath.Join(r.Root, "factory", "bin", "factory"))
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("factory command not found: %s", script)
	}
	if !r.begin() {
		return nil, cmp.Or(ctx.Err(), context.Canceled)
	}
	defer r.calls.Done()
	timeout := cmp.Or(r.Timeout, factoryTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.python(), script, "status", "--json")
	cmd.Dir = repoPath
	cmd.WaitDelay = time.Second
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return nil, ErrPython
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("factory status timed out after %s", timeout)
		case ctx.Err() != nil:
			return nil, ctx.Err()
		}
		msg := strings.TrimSpace(se.String())
		if i := strings.LastIndex(msg, "\n"); i >= 0 {
			msg = strings.TrimSpace(msg[i+1:])
		}
		return nil, errors.New(cmp.Or(strings.TrimPrefix(msg, "factory: "), err.Error()))
	}
	var out FactoryStatus
	if err := json.Unmarshal(so.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode factory output: %w", err)
	}
	return &out, nil
}

// FactorySettingsPath is the factory's settings file.
func FactorySettingsPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "factory", "settings.json")
}

// readSettings returns the file's keys with their values as written; a missing file is
// no keys, and anything that is not a JSON object is an error naming the path.
func readSettings(path string) (map[string]json.RawMessage, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("%s is not a JSON object", path)
	}
	return obj, nil
}

// ReadWorktreeCap is the cap the factory would use: the file's worktree_cap, else the default.
func ReadWorktreeCap(path string) (int, error) {
	obj, err := readSettings(path)
	if err != nil {
		return 0, err
	}
	raw, ok := obj["worktree_cap"]
	if !ok {
		return defaultWorktreeCap, nil
	}
	n, err := strconv.Atoi(string(raw))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s: worktree_cap must be an integer of at least 1", path)
	}
	return n, nil
}

// WriteWorktreeCap sets worktree_cap and leaves every other key's value as it was. The
// file is replaced atomically, keeps its permission bits, and is never written when it
// is not a JSON object.
func WriteWorktreeCap(path string, n int) error {
	obj, err := readSettings(path)
	if err != nil {
		return err
	}
	obj["worktree_cap"] = json.RawMessage(strconv.Itoa(n))
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out bytes.Buffer
	out.WriteString("{\n")
	for i, k := range keys {
		name, _ := json.Marshal(k)
		fmt.Fprintf(&out, "  %s: %s", name, obj[k])
		if i < len(keys)-1 {
			out.WriteByte(',')
		}
		out.WriteByte('\n')
	}
	out.WriteString("}\n")

	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
