package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// View renders the current frame.
func (m *model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "ccmon"
	if !m.th.NoColor {
		// Bubble Tea sets the terminal's own background too, so any cell the frame
		// does not cover (and the margins on resize) match the theme.
		v.BackgroundColor = m.th.Bg
		v.ForegroundColor = m.th.Text
	}
	return v
}

// Render draws exactly m.h lines of exactly m.w cells, joined by newlines.
func (m *model) Render() string {
	return strings.Join(m.frame(), "\n")
}

func (m *model) frame() []string {
	p := m.th.on(m.th.Bg)
	w, h := m.w, m.h
	if w < minWidth || h < minHeight {
		return m.smallScreen(p)
	}
	switch {
	case m.fatal != nil:
		return m.fatalScreen(p)
	case m.help:
		return m.helpScreen(p)
	case m.loading && len(m.sessions) == 0 && m.refreshErr == "":
		return m.loadingScreen(p)
	case len(m.sessions) == 0 && m.refreshErr != "":
		return m.errorScreen(p)
	}
	lines := []string{m.header(p), m.subheader(p)}
	var body block
	bh := h - 3
	switch m.view {
	case viewSession:
		body = m.sessionBody(p, w, bh)
	case viewAgents:
		body = m.agentsBody(p, w, bh)
	default:
		body = m.dashBody(p, w, bh)
	}
	for i := 0; i < bh; i++ {
		if i < len(body) {
			lines = append(lines, fit(body[i], w, p))
		} else {
			lines = append(lines, p.sp(w))
		}
	}
	lines = append(lines, m.footer(p))
	return lines
}

// ---- header / sub-header / footer -------------------------------------------------

func (m *model) header(p paint) string {
	th := m.th
	bar := p.on(th.Surface)
	title := bar.gradient(" ccmon ", true)
	tab := func(n int, label string, v viewID, short string) string {
		txt := fmt.Sprintf("%d %s", n, label)
		if m.w < 100 {
			txt = fmt.Sprintf("%d %s", n, short)
		}
		if m.view == v {
			if th.NoColor {
				return "[" + txt + "]"
			}
			return p.on(th.Raised).b(" "+txt+" ", th.Text)
		}
		return bar.t(" "+txt+" ", th.Muted)
	}
	tabs := tab(1, "Dashboard", viewDashboard, "Dash") + tab(2, "Session", viewSession, "Sess") + tab(3, "Agents", viewAgents, "Agents")

	var chips []string
	for i, r := range ranges {
		if i == m.rangeIdx {
			if th.NoColor {
				chips = append(chips, "["+r.label+"]")
			} else {
				chips = append(chips, p.on(mix(th.Surface, th.Purple, 0.3)).b(" "+r.label+" ", th.Purple))
			}
		} else {
			chips = append(chips, bar.t(" "+r.label+" ", th.Faint))
		}
	}
	rangeStr := strings.Join(chips, "")

	live := m.liveCount()
	liveStr := bar.t("○ idle", th.Faint)
	if live > 0 {
		liveStr = bar.dot(th.Green, m.pulse()) + bar.b(fmt.Sprintf(" %d live", live), th.Green)
	}
	clock := ""
	if !m.lastUpdate.IsZero() {
		clock = bar.t(m.lastUpdate.Local().Format("15:04:05"), th.Muted)
	}
	state := ""
	switch {
	case m.refreshErr != "" && len(m.sessions) > 0:
		state = bar.b("⚠ stale", th.Yellow)
	case m.loading && len(m.sessions) > 0:
		state = bar.t(spin(m.spinFrame())+" reloading", th.Blue)
	}
	cands := [][]string{
		{rangeStr, liveStr, state, clock},
		{rangeStr, liveStr, state},
		{rangeStr, liveStr},
		{rangeStr},
	}
	left := title + tabs
	for _, c := range cands {
		var parts []string
		for _, s := range c {
			if s != "" {
				parts = append(parts, s)
			}
		}
		right := strings.Join(parts, bar.sp(2)) + bar.sp(1)
		if width(left)+width(right)+1 <= m.w {
			return left + bar.sp(m.w-width(left)-width(right)) + right
		}
	}
	return fit(left, m.w, bar)
}

func (m *model) subheader(p paint) string {
	th := m.th
	var left string
	switch {
	case m.filterIn:
		left = p.b("/ ", th.Purple) + p.t(m.filter, th.Text) + p.t("█", th.Purple) + p.t("  enter to keep · esc to clear", th.Faint)
	case m.view == viewSession:
		name := m.selID
		if s := m.sessions[m.selID]; s != nil {
			name = s.Project + " › " + short8(s.ID)
		}
		left = p.t("Dashboard › ", th.Faint) + p.b(name, th.Text)
	case m.view == viewAgents:
		n := 0
		if m.agents != nil {
			n = m.agents.Sessions
		}
		left = p.b("Agents", th.Text) + p.t(fmt.Sprintf("  per agent type across %d sessions in %s", n, ranges[m.rangeIdx].label), th.Muted)
	default:
		n := len(m.visible())
		left = p.b("Sessions", th.Text) + p.t(fmt.Sprintf("  %d in %s · sorted by %s", n, ranges[m.rangeIdx].label, m.sortBy), th.Muted)
		if m.filter != "" {
			left += p.t("  · filter ", th.Faint) + p.b(m.filter, th.Purple)
		}
		if m.follow {
			left += p.t("  · ", th.Faint) + p.b("following", th.Pink)
		}
	}
	right := ""
	if m.toast.text != "" {
		col := th.Teal
		if m.toast.err {
			col = th.Red
		}
		right = p.pill(trunc(m.toast.text, max(10, m.w/2)), col)
	}
	left = trunc(left, m.w-width(right)-3)
	pad := m.w - 1 - width(left) - width(right)
	if pad < 0 {
		pad = 0
	}
	return fit(p.sp(1)+left+p.sp(pad)+right, m.w, p)
}

type hint struct{ key, desc string }

func (m *model) footer(p paint) string {
	th := m.th
	bar := p.on(th.Surface)
	var hs []hint
	switch m.view {
	case viewSession:
		hs = []hint{{"↑↓", "dispatch"}, {"enter", "detail"}, {"esc", "back"}, {"f", "follow"}, {"tab", "views"}, {"?", "help"}, {"q", "quit"}}
	case viewAgents:
		hs = []hint{{"↑↓", "move"}, {"r", "range"}, {"tab", "views"}, {"esc", "back"}, {"?", "help"}, {"q", "quit"}}
	default:
		hs = []hint{{"↑↓", "move"}, {"enter", "open"}, {"/", "filter"}, {"s", "sort"}, {"r", "range"}, {"f", "follow"}, {"tab", "views"}, {"?", "help"}, {"q", "quit"}}
	}
	if m.filterIn {
		hs = []hint{{"type", "to filter"}, {"enter", "keep"}, {"esc", "clear"}}
	}
	build := func(hs []hint) string {
		var parts []string
		for _, h := range hs {
			parts = append(parts, bar.b(h.key, th.Purple)+bar.t(" "+h.desc, th.Muted))
		}
		return bar.sp(1) + strings.Join(parts, bar.t("  ·  ", th.Faint))
	}
	for len(hs) > 2 && width(build(hs)) > m.w {
		// drop from the middle, always keeping the last two (help, quit)
		i := len(hs) - 3
		hs = append(hs[:i:i], hs[i+1:]...)
	}
	line := build(hs)
	right := ""
	if m.follow {
		right = bar.dot(th.Pink, m.pulse()) + bar.t(" follow ", th.Pink)
	}
	if width(line)+width(right)+2 <= m.w {
		return line + bar.sp(m.w-width(line)-width(right)) + right
	}
	return fit(line, m.w, bar)
}

func short8(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ---- full-screen states -----------------------------------------------------------

func (m *model) smallScreen(p paint) []string {
	th := m.th
	msg := block{
		p.gradient("ccmon", true),
		"",
		p.b("Make the window a little larger", th.Text),
		p.t(fmt.Sprintf("need at least %dx%d, have %dx%d", minWidth, minHeight, m.w, m.h), th.Muted),
	}
	if m.h < 4 {
		msg = block{p.t("larger window please", th.Text)}
	}
	return m.fitScreen(p.center(msg, max(m.w, 1), max(m.h, 1)), p)
}

// fitScreen guarantees exactly h lines of w cells.
func (m *model) fitScreen(b block, p paint) []string {
	out := make([]string, m.h)
	for i := range out {
		if i < len(b) {
			out[i] = fit(b[i], m.w, p)
		} else {
			out[i] = p.sp(m.w)
		}
	}
	return out
}

func (m *model) loadingScreen(p paint) []string {
	th := m.th
	bw := min(36, m.w-8)
	// an indeterminate bar: a bright gradient comet sliding over the track
	pos := 0
	if !m.opts.Static {
		pos = int(m.now().UnixMilli()/60) % (bw + 10)
	}
	var sb strings.Builder
	for i := 0; i < bw; i++ {
		d := pos - 5 - i
		if d < 0 {
			d = -d
		}
		if th.NoColor {
			if d <= 3 {
				sb.WriteString("█")
			} else {
				sb.WriteString("░")
			}
			continue
		}
		k := 1.0 - float64(d)/6
		if k < 0 {
			k = 0
		}
		sb.WriteString(p.t("━", mix(th.Border, th.gradientAt(float64(i)/float64(max(1, bw-1))), k)))
	}
	msg := block{
		p.gradient("c c m o n", true),
		"",
		sb.String(),
		"",
		p.t(spin(m.spinFrame())+" ", th.Purple) + p.t("reading transcripts…", th.Text),
		p.t("the first load of a large history takes a few seconds", th.Faint),
	}
	return m.fitScreen(p.center(msg, m.w, m.h), p)
}

func (m *model) fatalScreen(p paint) []string {
	th := m.th
	lines := []string{
		p.b("ccmon-tui can't reach the ccmon CLI", th.Red),
		"",
	}
	for _, l := range wrapPlain(m.fatal.Error(), min(m.w-12, 64)) {
		lines = append(lines, p.t(l, th.Text))
	}
	lines = append(lines, "",
		p.t("It needs Python 3.11+ on PATH and the repo's ccmon/ package.", th.Muted),
		p.t("Set CCMON_ROOT to the repo root if the binary was moved.", th.Muted),
		"", p.b("q", th.Purple)+p.t(" quit", th.Muted))
	bw := 0
	for _, l := range lines {
		bw = max(bw, width(l))
	}
	bw = min(m.w-4, bw+4)
	return m.fitScreen(p.center(p.boxed("error", lines, bw, len(lines)+2, th.Red), m.w, m.h), p)
}

func (m *model) errorScreen(p paint) []string {
	th := m.th
	lines := []string{p.b("Could not load sessions", th.Red), ""}
	for _, l := range wrapPlain(m.refreshErr, min(m.w-12, 64)) {
		lines = append(lines, p.t(l, th.Text))
	}
	lines = append(lines, "", p.b("r", th.Purple)+p.t(" retry   ", th.Muted)+p.b("q", th.Purple)+p.t(" quit", th.Muted))
	bw := 0
	for _, l := range lines {
		bw = max(bw, width(l))
	}
	bw = min(m.w-4, bw+4)
	return m.fitScreen(p.center(p.boxed("error", lines, bw, len(lines)+2, th.Red), m.w, m.h), p)
}

func (m *model) helpScreen(p paint) []string {
	th := m.th
	row := func(k, d string) string {
		return p.b(cell(k, 14, false), th.Purple) + p.t(d, th.Text)
	}
	lines := []string{
		p.gradient("ccmon", true) + p.t("  keys", th.Muted),
		"",
		p.b("Move", th.Yellow),
		row("j k ↑ ↓", "move the cursor (mouse wheel too)"),
		row("g G", "jump to first / last"),
		row("enter", "open the selected session"),
		row("esc backspace", "go back"),
		"",
		p.b("Views", th.Yellow),
		row("tab shift+tab", "next / previous view"),
		row("1 2 3", "dashboard · session · agents"),
		"",
		p.b("Data", th.Yellow),
		row("r", "cycle range: 24h 7d 30d all"),
		row("s", "sort: recent · tokens · subagent share"),
		row("/", "filter sessions by project or id"),
		row("f", "follow the most recently active session"),
		"",
		row("?", "toggle this help"),
		row("q ctrl+c", "quit"),
		"",
		p.t("A dot pulses on sessions written in the last 30 seconds.", th.Faint),
	}
	bw := 0
	for _, l := range lines {
		bw = max(bw, width(l))
	}
	bw = min(m.w-2, bw+4)
	bh := min(m.h, len(lines)+2)
	return m.fitScreen(p.center(p.boxed("help", lines, bw, bh, th.Pink), m.w, m.h), p)
}

// wrapPlain word-wraps plain text to w cells.
func wrapPlain(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		for _, word := range strings.Fields(para) {
			for width(word) > w {
				cut := trunc(word, w)
				cut = strings.TrimSuffix(cut, "…")
				if cut == "" {
					break
				}
				if cur != "" {
					out = append(out, cur)
					cur = ""
				}
				out = append(out, cut)
				word = word[len(cut):]
			}
			switch {
			case cur == "":
				cur = word
			case width(cur)+1+width(word) <= w:
				cur += " " + word
			default:
				out = append(out, cur)
				cur = word
			}
		}
		out = append(out, cur)
	}
	return out
}

var _ = time.Second
