package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"ccmon/internal/data"
)

const maxLanes = 6

type sessLayout struct {
	lanes        int
	laneH        int
	listH, listW int
	detailW      int // side detail pane (0 = none)
	detailH      int // stacked detail pane below the list (0 = none)
}

func (m *model) sessLayout() sessLayout {
	var l sessLayout
	bodyH := m.h - 3
	lanes := 0
	if d := m.details[m.selID]; d != nil {
		lanes = min(maxLanes, len(laneOrder(d)))
	}
	lanes = max(lanes, 1)
	// lanes grow taller when the dispatch list needs less than its share
	nDisp := 0
	if d := m.details[m.selID]; d != nil {
		nDisp = len(d.Dispatches)
	}
	wantList := min(max(nDisp, 4), 14) + 3
	l.laneH = 1
	for l.laneH < 3 && bodyH-(2+2+lanes*(l.laneH+1)+3) >= wantList+1 {
		l.laneH++
	}
	fixed := 2 + 2 + (lanes*l.laneH + 3) // header, agent bar, timeline box
	rest := bodyH - fixed
	l.lanes = lanes
	l.listW = m.w
	switch {
	case m.detailOnly:
		l.listH = rest
		l.detailW = m.w
	case m.w >= 120:
		l.detailW = 46
		l.listW = m.w - l.detailW - 1
		l.listH = rest
	case m.h >= 34:
		l.detailH = 10
		l.listH = rest - l.detailH
	default:
		l.listH = rest
	}
	return l
}

// sessRows is how many dispatch rows fit.
func (m *model) sessRows() int { return max(1, m.sessLayout().listH-3) }

// laneOrder lists the agent types of a session by first start.
func laneOrder(d *data.Session) []string {
	first := map[string]time.Time{}
	var names []string
	for _, x := range d.Dispatches {
		t := time.Time{}
		if x.Start != nil {
			t = *x.Start
		}
		if f, ok := first[x.AgentType]; !ok {
			first[x.AgentType] = t
			names = append(names, x.AgentType)
		} else if !t.IsZero() && (f.IsZero() || t.Before(f)) {
			first[x.AgentType] = t
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return first[names[i]].Before(first[names[j]]) })
	return names
}

func (m *model) sessionBody(p paint, w, h int) block {
	th := m.th
	s := m.sessions[m.selID]
	if s == nil {
		return p.center(block{p.t("No session selected — press esc and pick one.", th.Muted)}, w, h)
	}
	d := m.details[s.ID]
	l := m.sessLayout()
	out := m.sessionHeader(p, s, w)
	out = append(out, m.agentBar(p, s, w)...)
	if d == nil {
		out = append(out, p.sp(w))
		msg := p.t(spin(m.spinFrame())+" ", th.Purple) + p.t("loading dispatches…", th.Muted)
		for len(out) < h {
			out = append(out, p.sp(w))
		}
		out[len(out)/2] = p.sp(max(0, (w-20)/2)) + msg
		return out
	}
	out = append(out, m.timeline(p, s, d, w, l.lanes, l.laneH)...)
	cur := min(m.dcursor, max(0, len(d.Dispatches)-1))
	switch {
	case m.detailOnly:
		out = append(out, m.detailBox(p, d, cur, w, l.listH)...)
	case l.detailW > 0:
		need := min(len(d.Dispatches), 14) + 3
		listH := l.listH
		if rest := l.listH - need; rest >= 7 {
			listH = need
		}
		list := m.dispatchList(p, d, l.listW, listH, cur)
		if listH < l.listH {
			list = append(list, m.signalsBox(p, s, d, l.listW, l.listH-listH)...)
		}
		det := m.detailBox(p, d, cur, l.detailW, l.listH)
		out = append(out, joinCols(p, l.listH, 1, []int{l.listW, l.detailW}, list, det)...)
	case l.detailH > 0:
		out = append(out, m.dispatchList(p, d, w, l.listH, cur)...)
		out = append(out, m.detailBox(p, d, cur, w, l.detailH)...)
	default:
		out = append(out, m.dispatchList(p, d, w, l.listH, cur)...)
	}
	return out
}

func (m *model) sessionHeader(p paint, s *data.Session, w int) block {
	th := m.th
	span := ""
	if s.Start != nil && s.End != nil {
		span = fmt.Sprintf("%s → %s · %s", s.Start.Local().Format("Jan 02 15:04"), s.End.Local().Format("15:04"), FmtDuration(s.End.Sub(*s.Start)))
	}
	dot := p.t("○", th.Faint)
	if m.isLive(s) {
		dot = p.dot(th.Green, m.pulse())
	}
	pills := m.signalPills(p, s, w/2)
	if s.Repeats+s.ReviewLoops+s.WarnCount == 0 && s.Malformed == 0 {
		pills = p.pill("clean", th.Green)
	}
	mk := func(id string, withSpan bool) string {
		l := p.sp(1) + dot + p.sp(1) + p.b(id, th.Text) + p.t("  "+s.Project, th.Purple)
		if withSpan && span != "" {
			l += p.t("  "+span, th.Muted)
		}
		return l
	}
	l1 := mk(s.ID, true)
	for _, c := range []struct {
		id   string
		span bool
	}{{s.ID, true}, {short8(s.ID), true}, {short8(s.ID), false}} {
		l1 = mk(c.id, c.span)
		if width(l1)+width(pills)+2 <= w {
			break
		}
	}
	l1 = trunc(l1, w-width(pills)-2)
	l1 = l1 + p.sp(w-width(l1)-width(pills)-1) + pills

	tok := float64(s.Tokens.Total)
	if rc := m.rowTok[s.ID]; rc != nil {
		tok = rc.Value()
	}
	l2 := p.sp(1) + p.b(Human(tok), th.Text) + p.t(" tokens", th.Muted) +
		p.t("   main ", th.Muted) + p.b(HumanInt(s.Tokens.Main), th.Yellow) + p.t(fmt.Sprintf(" %.0f%%", s.Tokens.MainPct), th.Faint) +
		p.t("   subagents ", th.Muted) + p.b(HumanInt(s.Tokens.Subagents), th.Purple) + p.t(fmt.Sprintf(" %.0f%%", s.Tokens.SubagentPct), th.Faint)
	var in, out, cw, cr int64
	for _, u := range s.ByAgent {
		in += u.Input
		out += u.Output
		cw += u.CacheWrite
		cr += u.CacheRead
	}
	if extra := p.t(fmt.Sprintf("   in %s · out %s · cw %s · cr %s", HumanInt(in), HumanInt(out), HumanInt(cw), HumanInt(cr)), th.Faint); width(l2)+width(extra) <= w {
		l2 += extra
	}
	return block{fit(l1, w, p), fit(l2, w, p)}
}

func (m *model) agentBar(p paint, s *data.Session, w int) block {
	th := m.th
	rows, total := sortedUsage(s.ByAgent)
	var segs []seg
	var legend []string
	for _, r := range rows {
		col := th.AgentColor(r.k)
		segs = append(segs, seg{float64(r.v), col})
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total) * 100
		}
		legend = append(legend, p.t("■ ", col)+p.t(r.k, th.Text)+p.t(" "+pct(share)+" "+HumanInt(r.v), th.Muted))
	}
	bar := p.sp(1) + p.stack(w-2, segs)
	leg := p.sp(1) + strings.Join(legend, p.sp(3))
	return block{fit(bar, w, p), fit(leg, w, p)}
}

// ---- timeline -----------------------------------------------------------------------------

func dispatchBounds(x data.Dispatch, now time.Time) (start, end time.Time, ok bool) {
	if x.Start == nil {
		return start, end, false
	}
	start = *x.Start
	end = start
	if dur, has := x.Duration(now); has {
		end = start.Add(dur)
	} else if x.ReturnedAt != nil {
		end = *x.ReturnedAt
	}
	return start, end, true
}

func (m *model) timeline(p paint, s *data.Session, d *data.Session, w, lanes, laneH int) block {
	th := m.th
	in := p.on(th.Surface)
	now := m.now()
	names := laneOrder(d)
	hidden := 0
	if len(names) > lanes {
		hidden = len(names) - lanes
		names = names[:lanes]
	}
	t0, t1 := time.Time{}, time.Time{}
	grow := func(t time.Time) {
		if t.IsZero() {
			return
		}
		if t0.IsZero() || t.Before(t0) {
			t0 = t
		}
		if t1.IsZero() || t.After(t1) {
			t1 = t
		}
	}
	if s.Start != nil {
		grow(*s.Start)
	}
	if s.End != nil {
		grow(*s.End)
	}
	for _, x := range d.Dispatches {
		if st, en, ok := dispatchBounds(x, now); ok {
			grow(st)
			grow(en)
		}
	}
	if t1.Sub(t0) < time.Second {
		t1 = t0.Add(time.Second)
	}
	span := float64(t1.Sub(t0))
	labelW := 0
	for _, n := range names {
		labelW = max(labelW, width(n))
	}
	labelW = min(max(labelW, 6), 16)
	iw := w - 4
	tw := iw - labelW - 3
	if tw < 10 {
		tw = 10
	}
	x := func(t time.Time) int {
		v := int(float64(t.Sub(t0)) / span * float64(tw))
		return max(0, min(tw-1, v))
	}
	type pc struct {
		r      string
		fg     color.Color
		bright bool
	}
	cur := min(m.dcursor, max(0, len(d.Dispatches)-1))
	var lines []string
	frame := m.spinFrame() / 2
	for _, name := range names {
		cells := make([]pc, tw)
		col := th.AgentColor(name)
		for i := range cells {
			cells[i] = pc{r: " "}
		}
		for di, xd := range d.Dispatches {
			if xd.AgentType != name {
				continue
			}
			st, en, ok := dispatchBounds(xd, now)
			if !ok {
				continue
			}
			a, b := x(st), x(en)
			if b < a {
				b = a
			}
			for c := a; c <= b && c < tw; c++ {
				g, fg := "█", col
				switch xd.Status {
				case "running":
					if (c-frame%4+8)%4 < 2 {
						g = "█"
					} else {
						g = "▒"
					}
				case "failed":
					fg = th.Red
				case "no-run", "orphan":
					g, fg = "░", th.Faint
				}
				cells[c] = pc{r: g, fg: fg, bright: di == cur}
			}
		}
		var sb strings.Builder
		for _, c := range cells {
			switch {
			case c.r == " ":
				sb.WriteString(in.t("┈", mix(th.Surface, th.Border, 0.8)))
				if th.NoColor {
					// replaced below
				}
			case c.bright:
				sb.WriteString(in.b(c.r, mix(c.fg, th.Text, 0.45)))
			default:
				sb.WriteString(in.t(c.r, c.fg))
			}
		}
		label := in.t("● ", col) + in.t(cell(name, labelW, false), th.Text)
		for k := 0; k < laneH; k++ {
			lbl := in.sp(labelW + 2)
			if k == laneH/2 {
				lbl = label
			}
			lines = append(lines, lbl+in.sp(1)+sb.String())
		}
	}
	if hidden > 0 {
		lines = append(lines, in.t(fmt.Sprintf("+%d more agent types", hidden), th.Faint))
	}
	// time axis
	axis := make([]rune, tw)
	for i := range axis {
		axis[i] = ' '
	}
	put := func(pos int, s string, anchor string) {
		r := []rune(s)
		start := pos
		switch anchor {
		case "mid":
			start = pos - len(r)/2
		case "end":
			start = pos - len(r)
		}
		start = max(0, min(tw-len(r), start))
		for i, c := range r {
			axis[start+i] = c
		}
	}
	fmtT := func(t time.Time) string { return t.Local().Format("15:04") }
	put(0, fmtT(t0), "start")
	put(tw-1, fmtT(t1), "end")
	if tw >= 36 {
		put(tw/2, fmtT(t0.Add(t1.Sub(t0)/2)), "mid")
	}
	lines = append(lines, in.sp(labelW+3)+in.t(string(axis), th.Muted))
	return p.boxed("Timeline", lines, w, len(lines)+2, th.Blue)
}

// ---- dispatch list ---------------------------------------------------------------------------

type dispCols struct {
	agent int
	flags int
	model int
	inout bool
	desc  int
}

func dispLayout(iw int, agentW int) dispCols {
	c := dispCols{agent: agentW}
	used := 2 + 8 + 1 + agentW + 1 + 7 + 1 + 6 // marker, time, agent, total, dur
	if iw >= 70 {
		c.flags = 24
		if iw >= 100 {
			c.flags = 28
		}
		used += c.flags + 1
	}
	if iw >= 90 {
		c.model = 11
		used += c.model + 1
	}
	if iw >= 108 {
		c.inout = true
		used += 14
	}
	c.desc = iw - used - 1
	if c.desc < 6 && c.flags > 0 {
		used -= c.flags + 1
		c.flags = 0
		c.desc = iw - used - 1
	}
	return c
}

func (m *model) dispatchList(p paint, d *data.Session, w, h, cursor int) block {
	th := m.th
	in := p.on(th.Surface)
	iw := w - 4
	agentW := 6
	for _, x := range d.Dispatches {
		agentW = max(agentW, width(x.AgentType)+2*max(0, x.Depth-1))
	}
	agentW = min(agentW, 16)
	c := dispLayout(iw, agentW)
	head := "  " + cell("TIME", 8, false) + " " + cell("AGENT", c.agent, false) + " "
	if c.model > 0 {
		head += cell("MODEL", c.model, false) + " "
	}
	if c.inout {
		head += cell("IN", 6, true) + " " + cell("OUT", 6, true) + " "
	}
	head += cell("TOTAL", 7, true) + " " + cell("DUR", 6, true)
	if c.flags > 0 {
		head += " " + cell("FLAGS", c.flags, false)
	}
	head += " DESCRIPTION"
	lines := []string{in.t(head, th.Faint)}
	rows := h - 3
	if len(d.Dispatches) == 0 {
		lines = append(lines, "", in.t("  This session has no subagent dispatches yet.", th.Muted))
	}
	for i := m.dtop; i < len(d.Dispatches) && i < m.dtop+rows; i++ {
		lines = append(lines, m.dispatchRow(d.Dispatches[i], i == cursor, c, iw, in))
	}
	title := fmt.Sprintf("Dispatches %d", len(d.Dispatches))
	box := p.boxed(title, lines, w, h, th.Pink)
	if len(d.Dispatches) > rows {
		box = m.scrollMark(p, box, m.dtop, rows, len(d.Dispatches))
	}
	return box
}

func (m *model) flagPills(q paint, x data.Dispatch, w int) string {
	th := m.th
	type pl struct {
		t string
		c color.Color
	}
	var pls []pl
	switch x.Status {
	case "running":
		pls = append(pls, pl{spin(m.spinFrame()) + " running", th.Blue})
	case "failed":
		pls = append(pls, pl{"failed", th.Red})
	case "no-run", "orphan":
		pls = append(pls, pl{x.Status, th.Faint})
	}
	for _, f := range x.Flags {
		col := th.Yellow
		switch {
		case f == "WARN":
			col = th.Red
		case f == "rechecked":
			col = th.Orange
		}
		pls = append(pls, pl{f, col})
	}
	var sb strings.Builder
	used := 0
	for i, x := range pls {
		pw := width(x.t) + 2
		if used+pw > w {
			if i < len(pls) && w-used >= 4 {
				sb.WriteString(q.t(fmt.Sprintf("+%d", len(pls)-i), th.Faint))
			}
			break
		}
		sb.WriteString(q.pill(x.t, x.c))
		sb.WriteString(q.sp(1))
		used += pw + 1
	}
	return sb.String()
}

func (m *model) dispatchRow(x data.Dispatch, selected bool, c dispCols, iw int, base paint) string {
	th := m.th
	bg := th.Surface
	if selected {
		bg = th.Raised
	}
	q := base.on(bg)
	marker := q.sp(2)
	if selected {
		marker = q.b("▌", th.Pink) + q.sp(1)
		if th.NoColor {
			marker = "> "
		}
	}
	ts := "-"
	if x.Start != nil {
		ts = x.Start.Local().Format("15:04:05")
	}
	indent := strings.Repeat("  ", max(0, x.Depth-1))
	ag := indent + x.AgentType
	line := marker + q.t(cell(ts, 8, false), th.Muted) + q.sp(1) + q.b(cell(ag, c.agent, false), th.AgentColor(x.AgentType)) + q.sp(1)
	if c.model > 0 {
		ms := make([]string, 0, len(x.Models))
		for _, mo := range x.Models {
			ms = append(ms, shortModel(mo))
		}
		mc := th.Muted
		if len(x.Models) > 0 {
			mc = th.ModelColor(x.Models[0])
		}
		line += q.t(cell(strings.Join(ms, ","), c.model, false), mc) + q.sp(1)
	}
	if c.inout {
		line += q.t(cell(HumanInt(x.Usage.Input), 6, true), th.Muted) + q.sp(1) + q.t(cell(HumanInt(x.Usage.Output), 6, true), th.Muted) + q.sp(1)
	}
	line += q.t(cell(HumanInt(x.Usage.Total), 7, true), th.Text) + q.sp(1)
	dur := "-"
	if dd, ok := x.Duration(m.now()); ok {
		dur = FmtDuration(dd)
	}
	line += q.t(cell(dur, 6, true), th.Muted)
	if c.flags > 0 {
		fl := m.flagPills(q, x, c.flags)
		line += q.sp(1) + fl + q.sp(max(0, c.flags-width(fl)))
	}
	desc := firstLine(x.Description)
	dc := th.Muted
	if selected {
		dc = th.Text
	}
	line += q.sp(1) + q.t(trunc(desc, max(0, c.desc)), dc)
	return fit(line, iw+1, q)
}

// ---- detail pane ---------------------------------------------------------------------------------

func (m *model) detailBox(p paint, d *data.Session, cur, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	if len(d.Dispatches) == 0 {
		return p.boxed("Detail", []string{in.t("Select a dispatch to see its detail.", th.Muted)}, w, h, th.Teal)
	}
	x := d.Dispatches[cur]
	iw := w - 4
	col := th.AgentColor(x.AgentType)
	var ls []string
	for _, l := range wrapPlain(firstLine(x.Description), iw) {
		ls = append(ls, in.b(l, th.Text))
	}
	if len(ls) > 3 {
		ls = ls[:3]
	}
	status := x.Status
	sc := th.Green
	switch status {
	case "running":
		sc = th.Blue
		status = spin(m.spinFrame()) + " running"
	case "failed":
		sc = th.Red
	case "no-run", "orphan":
		sc = th.Faint
	}
	dur := "-"
	if dd, ok := x.Duration(m.now()); ok {
		dur = FmtDuration(dd)
	}
	ls = append(ls, in.t("● ", col)+in.b(x.AgentType, col)+in.t("  ·  ", th.Faint)+in.b(status, sc)+in.t("  ·  "+dur, th.Muted))
	span := ""
	if x.Start != nil {
		span = x.Start.Local().Format("15:04:05")
		if x.ReturnedAt != nil {
			span += " → " + x.ReturnedAt.Local().Format("15:04:05")
		}
	}
	if x.Resumes > 0 {
		span += fmt.Sprintf("  (resumed ×%d)", x.Resumes)
	}
	ls = append(ls, in.t(span, th.Muted))
	var ms []string
	for _, mo := range x.Models {
		ms = append(ms, in.t("■ ", th.ModelColor(mo))+in.t(shortModel(mo), th.Text))
	}
	if len(ms) > 0 {
		ls = append(ls, in.t("models  ", th.Faint)+strings.Join(ms, in.sp(2)))
	}
	maxC := max(1, x.Usage.Total)
	cls := []struct {
		n string
		v int64
		c color.Color
	}{{"input", x.Usage.Input, th.Teal}, {"output", x.Usage.Output, th.Pink}, {"cache-w", x.Usage.CacheWrite, th.Orange}, {"cache-r", x.Usage.CacheRead, th.Blue}}
	bw := max(4, iw-9-8)
	ls = append(ls, "")
	for _, k := range cls {
		ls = append(ls, in.t(cell(k.n, 8, false), th.Faint)+in.bar(bw, float64(k.v)/float64(maxC), k.c, false)+in.t(cell(HumanInt(k.v), 8, true), th.Text))
	}
	ls = append(ls, in.t(cell("total", 8, false), th.Faint)+in.b(cell(HumanInt(x.Usage.Total), iw-8, false), th.Text))
	if fl := m.flagPills(in, x, iw); len(x.Flags) > 0 && fl != "" {
		ls = append(ls, "", fl)
	}
	if x.RepeatOf != nil {
		of := *x.RepeatOf
		for _, o := range d.Dispatches {
			if o.ToolUseID == of || o.AgentID == of {
				of = fmt.Sprintf("%s “%s”", o.AgentType, firstLine(o.Description))
				break
			}
		}
		score := ""
		if x.RepeatScore != nil {
			score = fmt.Sprintf("  score %.2f", *x.RepeatScore)
		}
		ls = append(ls, in.t("repeats ", th.Yellow)+in.t(trunc(of, iw-8-width(score)), th.Text)+in.t(score, th.Yellow))
	}
	if x.ReviewLoopsDispatch+x.ReviewLoopsResume > 0 {
		ls = append(ls, in.t("review loop: ", th.Yellow)+in.t(fmt.Sprintf("answered a verdict via dispatch ×%d, resume ×%d", x.ReviewLoopsDispatch, x.ReviewLoopsResume), th.Muted))
	}
	for _, wn := range x.Warnings {
		for i, l := range wrapPlain(wn, iw-2) {
			prefix := "  "
			if i == 0 {
				prefix = "⚠ "
			}
			ls = append(ls, in.t(prefix+l, th.Red))
		}
	}
	title := fmt.Sprintf("Dispatch %d/%d", cur+1, len(d.Dispatches))
	return p.boxed(title, ls, w, h, col)
}

// signalsBox summarizes the session-level signals and review-loop events.
func (m *model) signalsBox(p paint, s *data.Session, d *data.Session, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	iw := w - 4
	rate := "-"
	if s.Recheck.Rate != nil {
		rate = fmt.Sprintf("%.0f%%", *s.Recheck.Rate*100)
	}
	col := func(n int, c color.Color) color.Color {
		if n > 0 {
			return c
		}
		return th.Green
	}
	lines := []string{
		in.t("repeats ", th.Faint) + in.b(fmt.Sprint(s.Repeats), col(s.Repeats, th.Yellow)) +
			in.t("   review loops ", th.Faint) + in.b(fmt.Sprint(s.ReviewLoops), col(s.ReviewLoops, th.Yellow)) +
			in.t("   warnings ", th.Faint) + in.b(fmt.Sprint(s.WarnCount), col(s.WarnCount, th.Red)) +
			in.t("   re-check ", th.Faint) + in.b(rate, th.Text) +
			in.t(fmt.Sprintf(" (%d/%d)", s.Recheck.Rechecked, s.Recheck.ExplorersWithCitations), th.Faint) +
			in.t("   malformed ", th.Faint) + in.b(fmt.Sprint(s.Malformed), th.Text),
	}
	desc := func(id string) string {
		for _, x := range d.Dispatches {
			if x.AgentID == id || x.ToolUseID == id {
				return firstLine(x.Description)
			}
		}
		return ""
	}
	if len(s.ReviewLoopEvent) == 0 {
		lines = append(lines, "", in.t("No review loops: every reviewer verdict so far was clean.", th.Muted))
	}
	for _, e := range s.ReviewLoopEvent {
		at := "-"
		if e.VerdictAt != nil {
			at = e.VerdictAt.Local().Format("15:04:05")
		}
		lines = append(lines, in.t("↺ ", th.Yellow)+in.t(at, th.Muted)+in.t(" reviewer ", th.Pink)+in.t("CHANGES_NEEDED → ", th.Text)+
			in.t("coder ", th.Blue)+in.t("("+e.Via+") ", th.Muted)+in.t(trunc(desc(e.CoderAgentID), max(8, iw-48)), th.Faint))
	}
	if len(lines) > h-2 {
		lines = lines[:h-2]
	}
	return p.boxed("Signals", lines, w, h, th.Yellow)
}
