package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"isshoni/internal/data"
)

const (
	meterCells = 20 // beyond this many slots the meter is a bar
	baseMax    = 24
	prMax      = 24
	slugLeast  = 16
	nameLeast  = 12 // cells the repository name keeps before the meter shrinks
)

// factoryBody is the factory view: the settings line, then one section per repository,
// scrolled when it is taller than the window.
func (m *model) factoryBody(p paint, w, h int) block {
	lines := m.factoryLines(p, w, h)
	top := max(0, min(m.fac.top, len(lines)-h))
	out := slices.Clone(lines[top:min(len(lines), top+h)])
	if more := len(lines) - top - h; more > 0 {
		out[h-1] = fit(p.sp(1)+p.t(fmt.Sprintf("+%d more lines · ↓ to scroll", more+1), m.th.Faint), w, p)
	}
	return out
}

// factoryPositions is how many places the body can be scrolled to.
func (m *model) factoryPositions() int {
	h := m.h - 3
	return max(1, len(m.factoryLines(m.th.on(m.th.Bg), m.w, h))-h+1)
}

func (m *model) factoryLines(p paint, w, h int) block {
	th := m.th
	f := &m.fac
	lines := block{m.settingsLine(p, w), p.sp(w)}
	switch {
	case f.doneAt.IsZero():
		msg := p.t(spin(m.spinFrame())+" ", th.Purple) + p.t("asking the factory…", th.Muted)
		return append(lines, p.center(block{msg}, w, h-len(lines))...)
	case len(f.repos) == 0:
		msg := block{p.b("No factory tasks here yet", th.Text), ""}
		for _, l := range wrapPlain("Tasks appear when a dispatcher starts leads with /factory:dispatch in a repository seen in this range.", min(w-8, 60)) {
			msg = append(msg, p.t(l, th.Muted))
		}
		return append(lines, p.center(msg, w, h-len(lines))...)
	}
	var all []data.FactoryTask
	for _, r := range f.repos {
		if r.st != nil {
			all = append(all, r.st.Rows...)
		}
	}
	cols := m.factoryCols(p, w, all)
	for i, r := range f.repos {
		if i > 0 {
			lines = append(lines, p.sp(w))
		}
		lines = append(lines, m.repoHeader(p, r, w))
		switch {
		case r.err != "":
			lines = append(lines, p.sp(2)+p.t(trunc("⚠ "+r.err, w-3), th.Red))
		case len(r.st.Rows) == 0:
			lines = append(lines, p.sp(2)+p.t("no tasks", th.Faint))
		default:
			lines = append(lines, cols.header(p))
			for _, t := range sortedTasks(r.st.Rows) {
				lines = append(lines, m.taskRow(p, cols, t))
			}
		}
	}
	return lines
}

// ---- subheader and settings line ------------------------------------------------

func (m *model) factorySubheader(p paint) string {
	th := m.th
	f := &m.fac
	left := p.b("Factory", th.Text)
	var motion, blocked, review, merged, settled int
	reported := false
	for _, r := range f.repos {
		if r.st != nil {
			motion, blocked, review, merged, settled = motion+r.st.InMotion, blocked+r.st.Blocked, review+r.st.InReview, merged+r.st.Merged, settled+r.st.Settled
			reported = true
		}
	}
	totals := func(long bool) string {
		if !reported {
			return ""
		}
		dot := p.t(" · ", th.Faint)
		out := p.sp(2) + p.t(fmt.Sprintf("%d %s", motion, phaseLabel("motion", long)), th.Blue)
		if blocked > 0 {
			out += p.b(fmt.Sprintf(" (%d blocked)", blocked), th.Orange)
		}
		out += dot + p.t(fmt.Sprintf("%d %s", review, phaseLabel("review", long)), th.Yellow) + dot +
			p.t(fmt.Sprintf("%d %s", merged, phaseLabel("merged", long)), m.mergedColor())
		if settled > 0 {
			out += dot + p.t(fmt.Sprintf("%d settled", settled), th.Muted)
		}
		return out
	}
	lead := p.sp(2)
	if reported {
		lead = p.t("  · ", th.Faint)
	}
	tail := func(asOf string) string {
		switch {
		case f.busy && asOf == "":
			return lead + p.t(spin(m.spinFrame()), th.Blue)
		case f.busy:
			return lead + p.t(spin(m.spinFrame())+" refreshing", th.Blue)
		case !f.doneAt.IsZero():
			return lead + p.t(asOf+f.doneAt.Local().Format("15:04:05"), th.Faint)
		}
		return ""
	}
	if long := left + totals(true) + tail("as of "); m.w >= 100 && width(long) <= m.w-2 {
		return long
	}
	if short := left + totals(false) + tail("as of "); width(short) <= m.w-2 {
		return short
	}
	return left + totals(false) + tail("")
}

func (m *model) settingsLine(p paint, w int) string {
	th := m.th
	f := &m.fac
	key := func(k, desc string) string { return p.b(k, th.Purple) + p.t(" "+desc, th.Faint) }
	left := p.sp(1) + p.t("worktree cap  ", th.Muted)
	switch {
	case f.editing:
		left += p.t("[ ", th.Purple) + p.b(f.field, th.Text) + p.t("█", th.Purple) + p.t(" ]", th.Purple)
		if f.fieldErr != "" {
			left += p.sp(2) + p.t(f.fieldErr, th.Red)
		} else {
			left += p.t("  per repo", th.Muted) + p.sp(3) + key("enter", "save") + p.t(" · ", th.Faint) + key("esc", "cancel") + p.t(" · ", th.Faint) + key("↑↓", "adjust")
		}
	case f.capErr != "":
		left += p.t(f.capErr, th.Red) + p.sp(3) + key("c", "edit")
	case f.cap == 0:
		left += p.t("…", th.Faint) + p.t("  per repo", th.Muted)
	default:
		left += p.b(strconv.Itoa(f.cap), th.Text) + p.t("  per repo", th.Muted) + p.sp(3) + key("c", "edit")
	}
	if right := p.t(data.ShortPath(data.FactorySettingsPath()), th.Faint); width(left)+width(right)+3 <= w {
		left += p.sp(w-width(left)-width(right)-1) + right
	}
	return fit(left, w, p)
}

// ---- repository header and capacity meter -----------------------------------------

func (m *model) repoHeader(p paint, r factoryRepo, w int) string {
	th := m.th
	right := ""
	if st := r.st; st != nil {
		count := func(label string, n int, c color.Color) string {
			if n == 0 {
				c = th.Faint
			}
			return p.t(label+" ", th.Faint) + p.b(strconv.Itoa(n), c)
		}
		counts := p.sp(3) + count("review", st.InReview, th.Yellow) + p.sp(3) + count("merged", st.Merged, m.mergedColor())
		if st.Settled > 0 {
			counts += p.sp(3) + count("settled", st.Settled, th.Muted)
		}
		budget := w - 3 - (1 + min(nameLeast, width(r.name)))
		right = m.meter(p, st, budget-width(counts)) + counts
		if width(right) > budget {
			right = m.meter(p, st, budget) // the name comes before the other counts
		}
	}
	left := trunc(p.sp(1)+p.b(r.name, th.Text), max(1, w-width(right)-3))
	return fit(left+p.sp(max(1, w-width(left)-width(right)-1))+right, w, p)
}

// meter shows tasks in motion against the cap: one cell per slot, a bar when there are
// too many to count or no room for them within budget cells. Green below the cap, yellow
// at it, red above it; the cells of blocked tasks keep the blocked phase's look.
func (m *model) meter(p paint, st *data.FactoryStatus, budget int) string {
	th := m.th
	n, limit := st.InMotion, st.Cap
	col := th.Green
	switch {
	case n > limit:
		col = th.Red
	case n == limit:
		col = th.Yellow
	case n == 0:
		col = th.Muted
	}
	label := fmt.Sprintf("%d/%d", n, limit)
	room := min(meterCells, budget-len("in motion ")-1-len(label))
	var cells string
	if max(n, limit) > room {
		cells = p.bar(max(4, room), float64(n)/float64(max(1, limit)), col, false)
	} else {
		blocked := min(st.Blocked, n)
		cells = p.t(strings.Repeat("▲", blocked), th.Orange) + p.t(strings.Repeat("●", n-blocked), col) + p.t(strings.Repeat("○", max(0, limit-n)), th.Faint)
	}
	return p.t("in motion ", th.Faint) + cells + p.sp(1) + p.b(label, col)
}

func (m *model) mergedColor() color.Color { return mix(m.th.Green, m.th.Muted, 0.45) }

// ---- tasks ------------------------------------------------------------------------

var knownPhases = []string{"blocked", "motion", "review", "merged", "settled"}

// sortedTasks orders by phase (the known ones first, the rest by name), overtime first, then slug.
func sortedTasks(rows []data.FactoryTask) []data.FactoryTask {
	rows = slices.Clone(rows)
	rank := func(phase string) int {
		if i := slices.Index(knownPhases, phase); i >= 0 {
			return i
		}
		return len(knownPhases)
	}
	slices.SortFunc(rows, func(a, b data.FactoryTask) int {
		if c := cmp.Or(cmp.Compare(rank(a.Phase), rank(b.Phase)), cmp.Compare(a.Phase, b.Phase)); c != 0 {
			return c
		}
		if a.Overtime != b.Overtime {
			if a.Overtime {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Slug, b.Slug)
	})
	return rows
}

// phaseLabel names a phase, in full from 100 columns up.
func phaseLabel(phase string, long bool) string {
	if long {
		switch phase {
		case "motion":
			return "in motion"
		case "review":
			return "waiting for review"
		case "merged":
			return "merged, not cleaned"
		case "blocked":
			return "blocked, needs you"
		case "settled":
			return "settled, not cleaned"
		}
	}
	return phase
}

// phaseLook is the glyph, label and colour of a task's phase; the glyph of a task in
// motion turns while its session works.
func (m *model) phaseLook(t data.FactoryTask, long bool) (glyph, label string, c color.Color) {
	th := m.th
	label = phaseLabel(t.Phase, long)
	switch t.Phase {
	case "motion":
		glyph = "●"
		if t.Session == "working" {
			glyph = spin(m.spinFrame())
		}
		return glyph, label, th.Blue
	case "review":
		return "◆", label, th.Yellow
	case "merged":
		return "✓", label, m.mergedColor()
	case "blocked":
		return "▲", label, th.Orange
	case "settled":
		return "·", label, th.Faint
	}
	return "·", label, th.Muted
}

func prText(t data.FactoryTask) string {
	if t.PRURL == "" && t.Deliverable != "" {
		if _, link, ok := strings.Cut(t.Deliverable, "://"); ok {
			return link
		}
		return filepath.Base(t.Deliverable)
	}
	switch t.PRState {
	case "none":
		return "-"
	case "unknown":
		return "?"
	}
	num := ""
	if t.PRURL != "" {
		num = "#" + t.PRURL[strings.LastIndex(t.PRURL, "/")+1:]
	}
	state := t.PRState
	if (state == "open" && t.Phase == "review") || (state == "merged" && t.Phase == "merged") {
		state = ""
	}
	return cmp.Or(strings.TrimSpace(num+" "+state), "-")
}

// baseText shows a stacked task (based on another task's branch) as the task it sits on.
func baseText(base string) (text string, stacked bool) {
	if slug, ok := strings.CutPrefix(base, "worktree-"); ok {
		return "↳ " + slug, true
	}
	return strings.TrimPrefix(base, "origin/"), false
}

func flagPills(p paint, t data.FactoryTask) string {
	var ps []string
	if t.Overtime {
		ps = append(ps, p.pill("overtime", p.th.Red))
	}
	if t.Stale {
		ps = append(ps, p.pill("stale", p.th.Yellow))
	}
	return strings.Join(ps, p.sp(1))
}

// factoryCols are the column widths shared by every table; 0 is a dropped column.
type factoryCols struct{ slug, phase, elapsed, pr, base, flags int }

// factoryCols sizes the columns to the widest cell of any task and drops BASE, then PR,
// then ELAPSED when the slug would get less than slugLeast cells.
func (m *model) factoryCols(p paint, w int, tasks []data.FactoryTask) factoryCols {
	c := factoryCols{slug: 4, phase: 5, elapsed: 7, pr: 2, base: 4, flags: 5}
	for _, t := range tasks {
		_, label, _ := m.phaseLook(t, m.w >= 100)
		base, _ := baseText(t.Base)
		c.slug = max(c.slug, width(t.Slug))
		c.phase = max(c.phase, 2+width(label))
		c.pr = max(c.pr, min(width(prText(t)), prMax))
		c.base = max(c.base, min(width(base), baseMax))
		c.flags = max(c.flags, width(flagPills(p, t)))
	}
	for step := 0; ; step++ {
		cc := c
		if step >= 1 {
			cc.base = 0
		}
		if step >= 2 {
			cc.pr = 0
		}
		if step >= 3 {
			cc.elapsed = 0
		}
		used := 2 + cc.phase + cc.flags + 4 // indent, and the gaps before PHASE and FLAGS
		for _, n := range []int{cc.elapsed, cc.pr, cc.base} {
			if n > 0 {
				used += n + 2
			}
		}
		if free := w - used; free >= min(c.slug, slugLeast) || step == 3 {
			cc.slug = max(1, min(c.slug, free))
			return cc
		}
	}
}

// join lays out one table line from its painted cells, each already padded to its column.
func (c factoryCols) join(p paint, slug, phase, elapsed, pr, base, flags string) string {
	parts := []string{slug, phase}
	for _, x := range []struct {
		w    int
		cell string
	}{{c.elapsed, elapsed}, {c.pr, pr}, {c.base, base}} {
		if x.w > 0 {
			parts = append(parts, x.cell)
		}
	}
	return p.sp(2) + strings.Join(append(parts, flags), p.sp(2))
}

func (c factoryCols) header(p paint) string {
	f := func(s string, w int, right bool) string { return p.t(cell(s, w, right), p.th.Faint) }
	return c.join(p, f("SLUG", c.slug, false), f("PHASE", c.phase, false), f("ELAPSED", c.elapsed, true), f("PR", c.pr, false), f("BASE", c.base, false), f("FLAGS", c.flags, false))
}

func (m *model) taskRow(p paint, c factoryCols, t data.FactoryTask) string {
	th := m.th
	glyph, label, col := m.phaseLook(t, m.w >= 100)
	elapsed, pr := "-", prText(t)
	if t.Elapsed != nil {
		elapsed = FmtDuration(time.Duration(*t.Elapsed) * time.Second)
	}
	prCol := th.Text
	if pr == "-" || pr == "?" {
		prCol = th.Faint
	}
	base, stacked := baseText(t.Base)
	baseCol := th.Faint
	if stacked {
		baseCol = th.Purple
	}
	phase := p.t
	if t.Phase == "blocked" {
		phase = p.b
	}
	return c.join(p,
		p.t(cell(t.Slug, c.slug, false), th.Text),
		fit(phase(glyph+" ", col)+phase(label, col), c.phase, p),
		p.t(cell(elapsed, c.elapsed, true), th.Muted),
		p.t(cell(pr, c.pr, false), prCol),
		p.t(cell(base, c.base, false), baseCol),
		fit(flagPills(p, t), c.flags, p))
}
