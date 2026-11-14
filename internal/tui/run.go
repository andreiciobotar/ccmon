package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"isshoni/internal/data"
)

// Run starts the interactive dashboard. Quitting cancels every python call and waits
// for them to end, so none outlives the TUI.
func Run(o Options) error { return runProgram(o) }

// runProgram is Run with extra program options, which tests use to drive it without a TTY.
func runProgram(o Options, popts ...tea.ProgramOption) error {
	ctx, cancel := context.WithCancel(context.Background())
	o.Ctx = ctx
	defer func() {
		cancel()
		if o.Runner != nil {
			o.Runner.Wait()
		}
	}()
	_, err := tea.NewProgram(New(o), popts...).Run()
	return err
}

// SnapshotOptions selects what Snapshot renders.
type SnapshotOptions struct {
	Options
	View    string // dashboard, session, agents, factory, kaizen, kaizen-flow, kaizen-board, help
	Session string // id prefix for the session view
	Row     int    // dashboard: cursor on this table row, 0 is the all row
	Fold    bool   // dashboard: collapse the repository the cursor is in
	W, H    int
}

// Snapshot loads data once and renders a single settled frame; no TTY needed. A
// session prefix is resolved by the python CLI, so an ambiguous one is an error here.
func Snapshot(so SnapshotOptions) (string, error) {
	o := so.Options
	o.Static = true
	m := New(o)
	m.w, m.h = so.W, so.H
	if m.fatal != nil {
		return "", m.fatal
	}
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	seen, _ := data.Scan(m.runner.Projects())
	res, err := m.runner.Sessions(ctx, ranges[m.rangeIdx].since)
	if err != nil {
		return "", err
	}
	m.seen = seen
	m.prices = res.Prices
	for i := range res.Sessions {
		s := &res.Sessions[i]
		s.LastChange = seen[s.ID]
		m.sessions[s.ID] = s
	}
	m.loading = false
	if ag, err := m.runner.Agents(ctx, ranges[m.rangeIdx].since); err == nil {
		m.agents = ag
	}
	detail := func(id string) error {
		d, err := m.runner.Session(ctx, id)
		if err != nil {
			return err
		}
		d.LastChange = seen[d.ID]
		m.details[d.ID] = d
		if m.sessions[d.ID] == nil {
			summary := *d
			summary.Dispatches = nil
			m.sessions[d.ID] = &summary
		}
		return nil
	}
	switch so.View {
	case "session":
		m.view = viewSession
		if so.Session != "" {
			if err := detail(so.Session); err != nil {
				return "", err
			}
			for id := range m.details {
				m.selID = id
			}
		} else if s := m.mostRecent(); s != nil {
			m.selID = s.ID
			if err := detail(s.ID); err != nil {
				return "", err
			}
		}
	case "agents":
		m.view = viewAgents
	case "factory":
		m.view = viewFactory
		m.applyFactory(m.factoryJob()())
	case "kaizen", "kaizen-flow", "kaizen-board":
		m.view = viewKaizen
		m.kz.page = map[string]kaizenPage{"kaizen": pagePulse, "kaizen-flow": pageFlow, "kaizen-board": pageBoard}[so.View]
		doc, err := m.runner.Kaizen(ctx, ranges[m.rangeIdx].since)
		if err != nil {
			return "", err
		}
		m.kz.doc, m.kz.doneAt, m.kz.doneRange = doc, m.now(), m.rangeIdx
	case "help":
		m.help = true
	default:
		for _, s := range m.sessions {
			if m.isLive(s) {
				if err := detail(s.ID); err != nil {
					return "", err
				}
			}
		}
	}
	m.normalizeSelection()
	if m.view == viewDashboard && so.Row > 0 && len(m.rows()) > 0 {
		rows := m.rows()
		m.selID = rows[min(so.Row, len(rows)-1)].key
	}
	if so.Fold {
		m.fold(true)
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

// preload fills a model for tests and renders without any I/O.
func (m *model) preload(sessions []*data.Session, details []*data.Session, agents *data.Agents) {
	m.seen, m.loaded = map[string]time.Time{}, true
	for _, s := range sessions {
		m.sessions[s.ID] = s
		m.seen[s.ID] = s.LastChange
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
