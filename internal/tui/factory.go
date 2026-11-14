package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"ccmon/internal/data"
)

const (
	factoryEvery = 30 * time.Second // from the end of one refresh to the start of the next
	factoryFresh = 5 * time.Second  // entering the view within this of a result starts no refresh
	capDigits    = 3
	capMax       = 999
)

// factoryRepo is one repository with a worktrees directory and what `factory status`
// said about it.
type factoryRepo struct {
	name, path string
	st         *data.FactoryStatus
	err        string // the status call failed
}

type factoryState struct {
	repos  []factoryRepo
	busy   bool // a refresh is in flight
	again  bool // asked for another while one was in flight
	doneAt time.Time
	top    int // first visible body line

	cap    int    // worktree cap shown on the settings line; 0 until known
	capErr string // the settings file cannot be used

	editing  bool
	field    string
	fieldErr string
}

type factoryMsg struct {
	repos  []factoryRepo
	cap    int
	capErr error
}

// factoryPaths lists the repositories of the sessions in this range, by name then path.
func (m *model) factoryPaths() []string {
	var paths []string
	for _, s := range m.sessions {
		if s.RepoPath != "" && !slices.Contains(paths, s.RepoPath) {
			paths = append(paths, s.RepoPath)
		}
	}
	slices.SortFunc(paths, func(a, b string) int {
		if c := strings.Compare(filepath.Base(a), filepath.Base(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return paths
}

// factoryJob is one refresh: it keeps the repositories that have a worktrees directory,
// asks the factory about each of them concurrently, and reads the settings file.
func (m *model) factoryJob() func() factoryMsg {
	r, ctx, paths := m.runner, m.ctx, m.factoryPaths()
	return func() factoryMsg {
		var repos []factoryRepo
		for _, p := range paths {
			if st, err := os.Stat(filepath.Join(p, ".claude", "worktrees")); err == nil && st.IsDir() {
				repos = append(repos, factoryRepo{name: filepath.Base(p), path: p})
			}
		}
		var wg sync.WaitGroup
		for i := range repos {
			wg.Go(func() {
				st, err := r.FactoryStatus(ctx, repos[i].path)
				if err != nil {
					repos[i].err = firstLine(err.Error())
					return
				}
				repos[i].st = st
			})
		}
		wg.Wait()
		n, err := data.ReadWorktreeCap(data.FactorySettingsPath())
		return factoryMsg{repos: repos, cap: n, capErr: err}
	}
}

// refreshFactory starts a refresh when the view is shown, none is in flight and the
// last result is older than minAge. A refresh asked for while one runs follows it.
func (m *model) refreshFactory(minAge time.Duration) tea.Cmd {
	f := &m.fac
	if m.view != viewFactory || m.runner == nil || m.fatal != nil {
		return nil
	}
	if f.busy {
		f.again = f.again || minAge == 0
		return nil
	}
	if !f.doneAt.IsZero() && m.now().Sub(f.doneAt) < minAge {
		return nil
	}
	f.busy = true
	job := m.factoryJob()
	return func() tea.Msg { return job() }
}

// applyFactory takes a refresh's result whole.
func (m *model) applyFactory(msg factoryMsg) tea.Cmd {
	f := &m.fac
	f.busy, f.doneAt, f.repos = false, m.now(), msg.repos
	f.cap, f.capErr = msg.cap, ""
	for _, r := range msg.repos {
		if r.st != nil {
			f.cap = r.st.Cap
			break
		}
	}
	if msg.capErr != nil {
		f.cap, f.capErr = 0, msg.capErr.Error()
	}
	m.fixScroll()
	if f.again {
		f.again = false
		return m.refreshFactory(0)
	}
	return nil
}

// factoryWorking is true while a task's session is working: its spinner turns.
func (m *model) factoryWorking() bool {
	for _, r := range m.fac.repos {
		if r.st == nil {
			continue
		}
		for _, t := range r.st.Rows {
			if t.Phase == "motion" && t.Session == "working" {
				return true
			}
		}
	}
	return false
}

// ---- the cap field -----------------------------------------------------------------

func (m *model) openCap() {
	n, err := data.ReadWorktreeCap(data.FactorySettingsPath())
	switch {
	case errors.Is(err, data.ErrWorktreeCap):
		n = data.DefaultWorktreeCap
	case err != nil:
		m.fac.capErr = err.Error()
		return
	}
	m.fac.editing, m.fac.field, m.fac.fieldErr = true, strconv.Itoa(n), ""
}

// capKey handles every key while the field is open; the caller has taken ctrl+c.
func (m *model) capKey(msg tea.KeyPressMsg) tea.Cmd {
	f := &m.fac
	f.fieldErr = ""
	switch {
	case key.Matches(msg, keys.CapCancel):
		f.editing = false
	case key.Matches(msg, keys.CapSave):
		return m.saveCap()
	case key.Matches(msg, keys.Erase):
		if n := len(f.field); n > 0 {
			f.field = f.field[:n-1]
		}
	case key.Matches(msg, keys.Up):
		m.adjustCap(1)
	case key.Matches(msg, keys.Down):
		m.adjustCap(-1)
	default:
		if t := msg.Key().Text; len(t) == 1 && t[0] >= '0' && t[0] <= '9' && len(f.field) < capDigits {
			f.field += t
		}
	}
	return nil
}

func (m *model) adjustCap(delta int) {
	n, _ := strconv.Atoi(m.fac.field)
	m.fac.field = strconv.Itoa(min(capMax, max(1, n+delta)))
}

func (m *model) saveCap() tea.Cmd {
	f := &m.fac
	n, _ := strconv.Atoi(f.field)
	if n < 1 {
		f.fieldErr = fmt.Sprintf("enter a whole number from 1 to %d", capMax)
		return nil
	}
	if err := data.WriteWorktreeCap(data.FactorySettingsPath(), n); err != nil {
		f.fieldErr = err.Error()
		return nil
	}
	f.editing, f.cap, f.capErr = false, n, ""
	m.setToast(fmt.Sprintf("worktree cap set to %d", n), false)
	return m.refreshFactory(0)
}
