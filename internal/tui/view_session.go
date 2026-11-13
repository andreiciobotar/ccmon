package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"ccmon/internal/data"
)

type sessLayout struct {
	plan         tlPlan
	listH, listW int
	detailW      int // side detail pane (0 = none)
	detailH      int // stacked detail pane below the list (0 = none)
}

const sessHeaderH = 3

func (m *model) sessLayout() sessLayout {
	var l sessLayout
	bodyH := m.h - 3
	tlH := 3 // an empty timeline: its axis in a box
	if s, d := m.sessions[m.selID], m.details[m.selID]; s != nil && d != nil {
		l.plan = m.timelinePlan(s, d, m.w, min(max(bodyH-sessHeaderH-2-8-3, 2), 8)) // keeps 8 rows for the list
		tlH = l.plan.boxH()
	}
	rest := bodyH - sessHeaderH - 2 - tlH
	l.listW, l.listH = m.w, rest
	switch {
	case m.detailOnly:
		l.detailW = m.w
	case m.w >= 120:
		l.detailW = min(max(m.w/4, 34), 56)
		l.listW = m.w - l.detailW - 1
	case m.h >= 34:
		l.detailH = 10
		l.listH = rest - l.detailH
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

// clock is "now" for a session's dispatches: the wall clock while the session is live,
// else the moment it ended, so a dispatch that never finished stops growing.
func (m *model) clock(s *data.Session) time.Time {
	if m.isLive(s) || s.End == nil {
		return m.now()
	}
	return *s.End
}

// unfinished is a dispatch still marked running in a session that is no longer live.
func (m *model) unfinished(s *data.Session, x data.Dispatch) bool {
	return x.Status == "running" && !m.isLive(s)
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
	out = append(out, m.timeline(p, s, d, l.plan, w)...)
	cur := min(m.dcursor, max(0, len(d.Dispatches)-1))
	switch {
	case m.detailOnly:
		out = append(out, m.detailBox(p, s, d, cur, w, l.listH)...)
	case l.detailW > 0:
		list := m.dispatchList(p, s, d, l.listW, l.listH, cur)
		det := m.detailBox(p, s, d, cur, l.detailW, l.listH)
		out = append(out, joinCols(p, l.listH, 1, []int{l.listW, l.detailW}, list, det)...)
	case l.detailH > 0:
		out = append(out, m.dispatchList(p, s, d, w, l.listH, cur)...)
		out = append(out, m.detailBox(p, s, d, cur, w, l.detailH)...)
	default:
		out = append(out, m.dispatchList(p, s, d, w, l.listH, cur)...)
	}
	return out
}

// spanText is "Jan 02 15:04 → 15:04 · 3m00s", with the end's date when it is another day.
func spanText(start, end time.Time) string {
	st, en := start.Local(), end.Local()
	endFmt := "15:04"
	if st.YearDay() != en.YearDay() || st.Year() != en.Year() {
		endFmt = "Jan 02 15:04"
	}
	return fmt.Sprintf("%s → %s · %s", st.Format("Jan 02 15:04"), en.Format(endFmt), FmtDuration(end.Sub(start)))
}

func (m *model) sessionHeader(p paint, s *data.Session, w int) block {
	th := m.th
	dot := p.t("○", th.Faint)
	if m.isLive(s) {
		dot = p.dot(th.Green, m.pulse())
	}
	span := ""
	if s.Start != nil && s.End != nil {
		span = spanText(*s.Start, *s.End)
	}
	l1 := p.sp(1) + dot + p.sp(1) + p.b(s.ID, th.Text) + p.t("  "+s.Project, th.Purple) + p.t("  "+span, th.Muted)
	if width(l1) > w {
		l1 = p.sp(1) + dot + p.sp(1) + p.b(short8(s.ID), th.Text) + p.t("  "+s.Project, th.Purple) + p.t("  "+span, th.Muted)
	}

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

	rate := "-"
	if s.Recheck.Rate != nil {
		rate = fmt.Sprintf("%.0f%%", *s.Recheck.Rate*100)
	}
	count := func(label string, n int, warn color.Color) string {
		c := th.Green
		if n > 0 {
			c = warn
		}
		return p.t(label+" ", th.Faint) + p.b(fmt.Sprint(n), c)
	}
	sep := p.t("  ·  ", th.Faint)
	l3 := p.sp(1) + strings.Join([]string{
		count("repeats", s.Repeats, th.Yellow),
		count("review loops", s.ReviewLoops, th.Yellow),
		count("warnings", s.WarnCount, th.Red),
		p.t("re-check ", th.Faint) + p.b(rate, th.Text) + p.t(fmt.Sprintf(" (%d/%d)", s.Recheck.Rechecked, s.Recheck.ExplorersWithCitations), th.Faint),
		p.t("malformed ", th.Faint) + p.b(fmt.Sprint(s.Malformed), th.Text),
	}, sep)
	return block{fit(l1, w, p), fit(l2, w, p), fit(l3, w, p)}
}

func (m *model) agentBar(p paint, s *data.Session, w int) block {
	th := m.th
	rows, total := sortedUsage(s.ByAgent)
	var segs []seg
	var legend []string
	for i, r := range rows {
		col := th.AgentColor(r.k)
		segs = append(segs, seg{float64(r.v), col})
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total) * 100
		}
		legend = append(legend, p.swatch(i, col)+p.t(" ", th.Text)+p.t(r.k, th.Text)+p.t(" "+pct(share)+" "+HumanInt(r.v), th.Muted))
	}
	bar := p.sp(1) + p.stack(w-2, segs)
	leg := p.sp(1) + strings.Join(legend, p.sp(3))
	return block{fit(bar, w, p), fit(leg, w, p)}
}

// ---- timeline -----------------------------------------------------------------------------

func dispatchBounds(x data.Dispatch, clock time.Time) (start, end time.Time, ok bool) {
	if x.Start == nil {
		return start, end, false
	}
	start = *x.Start
	end = start
	if dur, has := x.Duration(clock); has {
		end = start.Add(dur)
	} else if x.ReturnedAt != nil {
		end = *x.ReturnedAt
	}
	return start, end, true
}

// subLane is one timeline row: dispatches of one agent type that do not overlap.
type subLane struct {
	agent string
	first bool  // the first row of its agent type carries the label
	items []int // indexes into the session's dispatches, in start order
}

// tlPlan is the timeline laid out: the rows, the time scale and the label column.
type tlPlan struct {
	clock    time.Time
	lanes    []subLane
	hidden   int // rows left out for lack of height
	t0, t1   time.Time
	tw       int
	labelW   int
	multiDay bool
}

func (pl tlPlan) x(t time.Time) int {
	v := int(float64(t.Sub(pl.t0)) / float64(pl.t1.Sub(pl.t0)) * float64(pl.tw))
	return max(0, min(pl.tw-1, v))
}

// boxH is the height of the timeline box: rows, optional "more" line, axis, borders.
func (pl tlPlan) boxH() int {
	n := len(pl.lanes) + 1 + 2
	if pl.hidden > 0 {
		n++
	}
	return n
}

// timelinePlan puts every dispatch on a row. Dispatches of one agent type that overlap
// in time stack on separate rows, so two concurrent explorers are two bars.
func (m *model) timelinePlan(s, d *data.Session, w, maxRows int) tlPlan {
	pl := tlPlan{clock: m.clock(s)}
	grow := func(t time.Time) {
		if pl.t0.IsZero() || t.Before(pl.t0) {
			pl.t0 = t
		}
		if pl.t1.IsZero() || t.After(pl.t1) {
			pl.t1 = t
		}
	}
	if s.Start != nil {
		grow(*s.Start)
	}
	if s.End != nil {
		grow(*s.End)
	}
	for _, x := range d.Dispatches {
		if st, en, ok := dispatchBounds(x, pl.clock); ok {
			grow(st)
			grow(en)
		}
	}
	if pl.t1.Sub(pl.t0) < time.Second {
		pl.t1 = pl.t0.Add(time.Second)
	}
	l0, l1 := pl.t0.Local(), pl.t1.Local()
	pl.multiDay = l0.YearDay() != l1.YearDay() || l0.Year() != l1.Year()

	names := laneOrder(d)
	for _, n := range names {
		pl.labelW = max(pl.labelW, width(n))
	}
	pl.labelW = min(max(pl.labelW, 6), 16)
	pl.tw = max(10, w-4-pl.labelW-3)

	var all []subLane
	for _, name := range names {
		var idx []int
		for i, x := range d.Dispatches {
			if x.AgentType == name && x.Start != nil {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(i, j int) bool { return d.Dispatches[idx[i]].Start.Before(*d.Dispatches[idx[j]].Start) })
		var rows []subLane
		var ends []int
		for _, i := range idx {
			st, en, _ := dispatchBounds(d.Dispatches[i], pl.clock)
			a, b := pl.x(st), pl.x(en)
			k := 0
			for k < len(ends) && ends[k] >= a {
				k++
			}
			if k == len(ends) {
				ends = append(ends, 0)
				rows = append(rows, subLane{agent: name, first: k == 0})
			}
			ends[k] = max(a, b)
			rows[k].items = append(rows[k].items, i)
		}
		if len(rows) == 0 {
			rows = append(rows, subLane{agent: name, first: true})
		}
		all = append(all, rows...)
	}
	if len(all) > maxRows {
		pl.hidden = len(all) - (maxRows - 1)
		all = all[:maxRows-1]
	}
	pl.lanes = all
	return pl
}

func (m *model) timeline(p paint, s, d *data.Session, pl tlPlan, w int) block {
	th := m.th
	in := p.on(th.Surface)
	cur := min(m.dcursor, max(0, len(d.Dispatches)-1))
	live := m.isLive(s)
	frame := m.spinFrame() / 2
	var lines []string
	for _, lane := range pl.lanes {
		col := th.AgentColor(lane.agent)
		type pc struct {
			r      string
			fg     color.Color
			bright bool
		}
		cells := make([]pc, pl.tw)
		for _, di := range lane.items {
			xd := d.Dispatches[di]
			st, en, _ := dispatchBounds(xd, pl.clock)
			a, b := pl.x(st), pl.x(en)
			for c := a; c <= b && c < pl.tw; c++ {
				g, fg := "█", col
				switch {
				case xd.Status == "running" && live:
					g = "█"
					if (c-frame%4+8)%4 >= 2 {
						g = "▒"
					}
				case xd.Status == "running":
					g = "▒"
				case xd.Status == "failed":
					fg = th.Red
				case xd.Status == "no-run", xd.Status == "orphan":
					g, fg = "░", th.Faint
				}
				if di == cur && th.NoColor {
					g = "▓"
				}
				cells[c] = pc{r: g, fg: fg, bright: di == cur}
			}
		}
		var sb strings.Builder
		for _, c := range cells {
			switch {
			case c.r == "":
				sb.WriteString(in.t("┈", mix(th.Surface, th.Border, 0.8)))
			case c.bright:
				sb.WriteString(in.b(c.r, mix(c.fg, th.Text, 0.45)))
			default:
				sb.WriteString(in.t(c.r, c.fg))
			}
		}
		label := in.sp(pl.labelW + 2)
		if lane.first {
			label = in.t("● ", col) + in.t(cell(lane.agent, pl.labelW, false), th.Text)
		}
		lines = append(lines, label+in.sp(1)+sb.String())
	}
	if pl.hidden > 0 {
		lines = append(lines, in.t(fmt.Sprintf("+%d more rows", pl.hidden), th.Faint))
	}
	// time axis, with dates when the session spans more than one day
	axis := make([]rune, pl.tw)
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
		start = max(0, min(pl.tw-len(r), start))
		for i, c := range r {
			axis[start+i] = c
		}
	}
	layout := "15:04"
	if pl.multiDay {
		layout = "01-02 15:04"
	}
	fmtT := func(t time.Time) string { return t.Local().Format(layout) }
	put(0, fmtT(pl.t0), "start")
	put(pl.tw-1, fmtT(pl.t1), "end")
	if pl.tw >= 3*len(layout)+6 {
		put(pl.tw/2, fmtT(pl.t0.Add(pl.t1.Sub(pl.t0)/2)), "mid")
	}
	lines = append(lines, in.sp(pl.labelW+3)+in.t(string(axis), th.Muted))
	return p.boxed("Timeline", lines, w, pl.boxH(), th.Blue)
}

// ---- dispatch list ---------------------------------------------------------------------------

type pill struct {
	text string
	col  color.Color
}

// flagList is a dispatch's status and flags as pills.
func (m *model) flagList(s *data.Session, x data.Dispatch) []pill {
	th := m.th
	var pls []pill
	switch {
	case m.unfinished(s, x):
		pls = append(pls, pill{"unfinished", th.Orange})
	case x.Status == "running":
		pls = append(pls, pill{spin(m.spinFrame()) + " running", th.Blue})
	case x.Status == "failed":
		pls = append(pls, pill{"failed", th.Red})
	case x.Status == "no-run", x.Status == "orphan":
		pls = append(pls, pill{x.Status, th.Faint})
	}
	for _, f := range x.Flags {
		col := th.Yellow
		switch f {
		case "WARN":
			col = th.Red
		case "rechecked":
			col = th.Orange
		}
		pls = append(pls, pill{f, col})
	}
	return pls
}

// flagPills renders pills separated by single spaces within w cells; pills that do not
// fit are replaced by "+N".
func (m *model) flagPills(q paint, pls []pill, w int) string {
	var parts []string
	used := 0
	for i, x := range pls {
		pw := pillWidth(x.text)
		if used+pw > w {
			if i == 0 {
				parts = append(parts, q.pill(trunc(x.text, w-2), x.col))
			} else if w-used >= 3 {
				parts = append(parts, q.t(fmt.Sprintf("+%d", len(pls)-i), m.th.Faint))
			}
			break
		}
		parts = append(parts, q.pill(x.text, x.col))
		used += pw + 1
	}
	return strings.Join(parts, q.sp(1))
}

func pillsWidth(pls []pill) int {
	w := 0
	for _, x := range pls {
		w += pillWidth(x.text) + 1
	}
	return max(0, w-1)
}

type dispCols struct {
	agent, model, flags, desc int
	inout                     bool
}

const (
	timeW    = 8
	numW     = 6
	totalW   = 7
	durW     = 6
	minDescW = 14
)

// dispLayout sizes the columns from the data. When the width cannot hold them all with
// a readable description, the numeric in/out columns go first, then model, then flags.
func (m *model) dispLayout(iw int, s, d *data.Session) dispCols {
	var c dispCols
	c.agent = 6
	c.model = 5
	for _, x := range d.Dispatches {
		c.agent = max(c.agent, width(x.AgentType)+2*max(0, x.Depth-1))
		c.model = max(c.model, width(modelsText(x)))
		c.flags = max(c.flags, pillsWidth(m.flagList(s, x)))
	}
	c.agent = min(c.agent, 12)
	c.model = min(c.model, 10)
	c.flags = min(c.flags, 16)
	c.inout = true
	used := func() int {
		u := 2 + timeW + 1 + c.agent + 1 + totalW + 1 + durW
		if c.model > 0 {
			u += c.model + 1
		}
		if c.inout {
			u += 2 * (numW + 1)
		}
		if c.flags > 0 {
			u += c.flags + 1
		}
		return u
	}
	for c.desc = iw - used() - 1; c.desc < minDescW; c.desc = iw - used() - 1 {
		switch {
		case c.inout:
			c.inout = false
		case c.model > 0:
			c.model = 0
		case c.flags > 0:
			c.flags = 0
		default:
			c.desc = max(c.desc, 0)
			return c
		}
	}
	return c
}

func modelsText(x data.Dispatch) string {
	ms := make([]string, 0, len(x.Models))
	for _, mo := range x.Models {
		ms = append(ms, shortModel(mo))
	}
	return strings.Join(ms, ",")
}

func (m *model) dispatchList(p paint, s, d *data.Session, w, h, cursor int) block {
	th := m.th
	in := p.on(th.Surface)
	iw := w - 4
	c := m.dispLayout(iw, s, d)
	head := "  " + cell("TIME", timeW, false) + " " + cell("AGENT", c.agent, false) + " "
	if c.model > 0 {
		head += cell("MODEL", c.model, false) + " "
	}
	if c.inout {
		head += cell("IN", numW, true) + " " + cell("OUT", numW, true) + " "
	}
	head += cell("TOTAL", totalW, true) + " " + cell("DUR", durW, true)
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
		lines = append(lines, m.dispatchRow(s, d.Dispatches[i], i == cursor, c, iw, in))
	}
	title := fmt.Sprintf("Dispatches %d", len(d.Dispatches))
	box := p.boxed(title, lines, w, h, th.Pink)
	if len(d.Dispatches) > rows {
		box = m.scrollMark(p, box, m.dtop, rows, len(d.Dispatches))
	}
	return box
}

// durText is a dispatch's duration; "-" when it never finished.
func (m *model) durText(s *data.Session, x data.Dispatch) string {
	if m.unfinished(s, x) {
		return "-"
	}
	if dd, ok := x.Duration(m.now()); ok {
		return FmtDuration(dd)
	}
	return "-"
}

func (m *model) dispatchRow(s *data.Session, x data.Dispatch, selected bool, c dispCols, iw int, base paint) string {
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
	ag := strings.Repeat("  ", max(0, x.Depth-1)) + x.AgentType
	line := marker + q.t(cell(ts, timeW, false), th.Muted) + q.sp(1) + q.b(cell(ag, c.agent, false), th.AgentColor(x.AgentType)) + q.sp(1)
	if c.model > 0 {
		mc := th.Muted
		if len(x.Models) > 0 {
			mc = th.ModelColor(x.Models[0])
		}
		line += q.t(cell(modelsText(x), c.model, false), mc) + q.sp(1)
	}
	if c.inout {
		line += q.t(cell(HumanInt(x.Usage.Input), numW, true), th.Muted) + q.sp(1) + q.t(cell(HumanInt(x.Usage.Output), numW, true), th.Muted) + q.sp(1)
	}
	line += q.t(cell(HumanInt(x.Usage.Total), totalW, true), th.Text) + q.sp(1)
	line += q.t(cell(m.durText(s, x), durW, true), th.Muted)
	if c.flags > 0 {
		line += q.sp(1) + fit(m.flagPills(q, m.flagList(s, x), c.flags), c.flags, q)
	}
	dc := th.Muted
	if selected {
		dc = th.Text
	}
	line += q.sp(1) + q.t(trunc(firstLine(x.Description), max(0, c.desc)), dc)
	return fit(line, iw+1, q)
}

// ---- detail pane ---------------------------------------------------------------------------------

// matches reports whether the ids name dispatch x.
func matches(x data.Dispatch, agentID, toolUseID string) bool {
	return (agentID != "" && x.AgentID == agentID) || (toolUseID != "" && x.ToolUseID == toolUseID)
}

func (m *model) detailBox(p paint, s, d *data.Session, cur, w, h int) block {
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
	switch {
	case m.unfinished(s, x):
		sc, status = th.Orange, "unfinished"
	case x.Status == "running":
		sc, status = th.Blue, spin(m.spinFrame())+" running"
	case x.Status == "failed":
		sc = th.Red
	case x.Status == "no-run", x.Status == "orphan":
		sc = th.Faint
	}
	ls = append(ls, in.t("● ", col)+in.b(x.AgentType, col)+in.t("  ·  ", th.Faint)+in.b(status, sc)+in.t("  ·  "+m.durText(s, x), th.Muted))
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
	if len(x.Flags) > 0 {
		ls = append(ls, "", m.flagPills(in, m.flagList(s, x), iw))
	}
	desc := func(agentID, toolUseID string) string {
		for _, o := range d.Dispatches {
			if matches(o, agentID, toolUseID) {
				return firstLine(o.Description)
			}
		}
		return ""
	}
	if x.RepeatOf != nil {
		of := *x.RepeatOf
		if t := desc(of, of); t != "" {
			of = t
		}
		score := ""
		if x.RepeatScore != nil {
			score = fmt.Sprintf("  score %.2f", *x.RepeatScore)
		}
		ls = append(ls, in.t("repeats ", th.Yellow)+in.t(trunc(of, iw-8-width(score)), th.Text)+in.t(score, th.Yellow))
	}
	for _, e := range d.ReviewLoopEvent {
		at := "-"
		if e.VerdictAt != nil {
			at = e.VerdictAt.Local().Format("15:04:05")
		}
		var l string
		switch {
		case matches(x, e.ReviewerAgentID, e.ReviewerToolUseID):
			l = fmt.Sprintf("CHANGES_NEEDED at %s, answered by coder (%s): %s", at, e.Via, desc(e.CoderAgentID, e.CoderToolUseID))
		case matches(x, e.CoderAgentID, e.CoderToolUseID):
			l = fmt.Sprintf("answered a CHANGES_NEEDED verdict from %s (%s) at %s", firstNonEmpty(desc(e.ReviewerAgentID, e.ReviewerToolUseID), "reviewer"), e.Via, at)
		default:
			continue
		}
		for i, ln := range wrapPlain(l, iw-2) {
			prefix := "  "
			if i == 0 {
				prefix = "↺ "
			}
			ls = append(ls, in.t(prefix+ln, th.Yellow))
		}
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

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
