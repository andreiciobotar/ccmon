package tui

import (
	"fmt"
	"image/color"
	"strings"

	"ccmon/internal/data"
)

const cardH = 4 // three lines and a gap

func (m *model) agentRows() int {
	return max(1, (m.h-3-2)/cardH)
}

func (m *model) agentsBody(p paint, w, h int) block {
	th := m.th
	if m.agents == nil {
		msg := p.t(spin(m.spinFrame())+" ", th.Purple) + p.t("aggregating agents across the history…", th.Muted)
		return p.center(block{msg}, w, h)
	}
	rows := m.agents.Agents
	if len(rows) == 0 {
		return p.center(block{p.b("No agent activity in this range", th.Text), p.t("press r to widen the range", th.Muted)}, w, h)
	}
	var maxTot, maxTok, maxDur float64
	for _, a := range rows {
		maxTot = max(maxTot, float64(a.Usage.Total))
		if a.TokensP90 != nil {
			maxTok = max(maxTok, *a.TokensP90)
		}
		if a.DurationP90 != nil {
			maxDur = max(maxDur, *a.DurationP90)
		}
	}
	// model strip on top
	models, total := sortedUsage(m.agents.ByModel)
	out := block{m.mixStrip(p, "models", models, total, th.ModelColor, shortModel, w), p.sp(w)}
	per := (h - len(out)) / cardH
	cur := min(m.acursor, len(rows)-1)
	for i := m.atop; i < len(rows) && i < m.atop+per; i++ {
		out = append(out, m.agentCard(p, rows[i], i == cur, w, maxTot, maxTok, maxDur)...)
	}
	return out
}

var classColors = func(th *Theme) [4]color.Color {
	return [4]color.Color{th.Teal, th.Pink, th.Orange, th.Blue}
}

func (m *model) agentCard(p paint, a data.AgentRow, selected bool, w int, maxTot, maxTok, maxDur float64) block {
	th := m.th
	bg := th.Bg
	if selected {
		bg = th.Surface
	}
	q := p.on(bg)
	col := th.AgentColor(a.Agent)
	marker := q.sp(1)
	if selected {
		marker = q.b("▌", col)
		if th.NoColor {
			marker = ">"
		}
	}
	// line 1: name, dispatches, models, pills
	var ms []string
	for _, mo := range a.Models {
		ms = append(ms, q.t("■ ", th.ModelColor(mo))+q.t(shortModel(mo), th.Muted))
	}
	disp := fmt.Sprintf("%d dispatches", a.Dispatches)
	if a.Dispatches == 1 {
		disp = "1 dispatch"
	}
	if a.Agent == "main" {
		disp = "main session"
	}
	l1 := marker + q.sp(1) + q.t("● ", col) + q.b(a.Agent, col) + q.t("   "+disp, th.Text) + q.sp(3) + strings.Join(ms, q.sp(2))
	var pills []string
	if a.WarnCount > 0 {
		pills = append(pills, q.pill(fmt.Sprintf("WARN %d", a.WarnCount), th.Red))
	}
	if a.Repeats > 0 {
		pills = append(pills, q.pill(fmt.Sprintf("repeat %d", a.Repeats), th.Yellow))
	}
	if a.ReviewLoops > 0 {
		pills = append(pills, q.pill(fmt.Sprintf("loop %d", a.ReviewLoops), th.Yellow))
	}
	right := strings.Join(pills, q.sp(1))
	if right == "" && a.Agent != "main" {
		right = q.t("no repeats, loops or warnings", th.Faint)
	}
	l1 = trunc(l1, w-width(right)-3)
	l1 += q.sp(w-width(l1)-width(right)-1) + right

	// line 2: token classes
	cc := classColors(th)
	bw := min(32, max(10, w/4))
	segs := []seg{{float64(a.Usage.CacheRead), cc[3]}, {float64(a.Usage.CacheWrite), cc[2]}, {float64(a.Usage.Output), cc[1]}, {float64(a.Usage.Input), cc[0]}}
	bar := q.stack(bw, segs)
	cls := func(i int, name string, v int64) string {
		return q.swatch(i, cc[3-i]) + q.t(" "+name+" ", th.Faint) + q.t(HumanInt(v), cc[3-i])
	}
	l2 := q.sp(4) + q.t("usage ", th.Faint) + bar + q.sp(2) +
		cls(3, "in", a.Usage.Input) + q.sp(2) + cls(2, "out", a.Usage.Output) + q.sp(2) +
		cls(1, "cw", a.Usage.CacheWrite) + q.sp(2) + cls(0, "cr", a.Usage.CacheRead) +
		q.t("  = ", th.Faint) + q.b(HumanInt(a.Usage.Total), th.Text)

	// line 3: percentiles
	mini := min(12, max(3, (w-68)/4))
	mk := func(label string, v *float64, mx float64, f func(float64) string, c color.Color) string {
		if v == nil {
			return q.t(label+" ", th.Faint) + q.t("-", th.Faint)
		}
		frac := 0.0
		if mx > 0 {
			frac = *v / mx
		}
		return q.t(label+" ", th.Faint) + q.bar(mini, frac, c, false) + q.sp(1) + q.t(f(*v), th.Text)
	}
	tokF := func(v float64) string { return Human(v) }
	durF := func(v float64) string { return FmtDurationMS(&v) }
	l3 := q.sp(4) + mk("tok p50", a.TokensP50, maxTok, tokF, th.Purple) + q.sp(2) + mk("p90", a.TokensP90, maxTok, tokF, th.Pink) +
		q.sp(4) + mk("time p50", a.DurationP50, maxDur, durF, th.Teal) + q.sp(2) + mk("p90", a.DurationP90, maxDur, durF, th.Blue)
	if a.Agent == "main" {
		l3 = q.sp(4) + q.t("the orchestrating session itself · no dispatches", th.Faint)
	}
	return block{fit(l1, w, q), fit(l2, w, q), fit(l3, w, q), p.sp(w)}
}
