package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"ccmon/internal/data"
)

// DefaultProjects is where Claude Code keeps transcripts.
func DefaultProjects() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// Run starts the interactive dashboard.
func Run(o Options, projectsDir string) error {
	if o.Runner != nil && o.Runner.Check() == nil && projectsDir != "" {
		if ch, err := data.Watch(context.Background(), projectsDir, 15*time.Second); err == nil {
			o.Changes = ch
		}
	}
	p := tea.NewProgram(New(o))
	_, err := p.Run()
	return err
}

// SnapshotOptions selects what Snapshot renders.
type SnapshotOptions struct {
	Options
	View    string // dashboard, session, agents, help
	Session string // id prefix for the session view
	W, H    int
}

// Snapshot loads data once and renders a single settled frame; no TTY needed.
func Snapshot(so SnapshotOptions) (string, error) {
	o := so.Options
	o.Static = true
	m := New(o)
	m.w, m.h = so.W, so.H
	if m.fatal != nil {
		m.loading = false
		return m.finishSnapshot(), nil
	}
	ctx := context.Background()
	since := ranges[m.rangeIdx].since
	res, err := m.runner.Sessions(ctx, since)
	if err != nil {
		m.loading = false
		m.refreshErr = err.Error()
		return m.finishSnapshot(), nil
	}
	for i := range res.Sessions {
		s := res.Sessions[i]
		if t := data.StatMtime(s.Path); t > 0 {
			s.LastChange = time.Unix(0, t)
		}
		m.sessions[s.ID] = &s
	}
	m.loading = false
	if ag, err := m.runner.Agents(ctx, since); err == nil {
		m.agents = ag
	}
	switch so.View {
	case "session":
		m.view = viewSession
		id := so.Session
		for k := range m.sessions {
			if id != "" && strings.HasPrefix(k, id) {
				m.selID = k
			}
		}
		if id != "" && m.selID == "" {
			return "", fmt.Errorf("no session matching %q in range %s", id, ranges[m.rangeIdx].label)
		}
		if m.selID == "" {
			if s := m.mostRecent(); s != nil {
				m.selID = s.ID
			}
		}
		if m.selID != "" {
			if d, err := m.runner.Session(ctx, m.selID); err == nil {
				m.details[d.ID] = d
				if s := m.sessions[d.ID]; s != nil {
					s.Dispatches = nil
				}
			}
		}
	case "agents":
		m.view = viewAgents
	case "help":
		m.help = true
	}
	if m.selID == "" {
		m.normalizeSelection()
		if m.selID == "" {
			if s := m.mostRecent(); s != nil {
				m.selID = s.ID
			}
		}
	}
	return m.finishSnapshot(), nil
}

func (m *model) finishSnapshot() string {
	m.settle()
	m.lastUpdate = m.now()
	m.fixScroll()
	out := m.Render()
	if m.th.NoColor {
		out = ansi.Strip(out)
	}
	return out
}

// Preload fills a model for tests and renders without any I/O.
func (m *model) preload(sessions []*data.Session, details []*data.Session, agents *data.Agents) {
	for _, s := range sessions {
		m.sessions[s.ID] = s
	}
	for _, d := range details {
		m.details[d.ID] = d
	}
	m.agents = agents
	m.loading = false
	m.normalizeSelection()
	m.settle()
	m.fixScroll()
}
