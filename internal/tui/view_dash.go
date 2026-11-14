package tui

import (
	"fmt"
	"image/color"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"isshoni/internal/data"
)

type dashLayout struct {
	compactTiles bool
	sideW        int // side column of mix boxes; 0 puts one-line strips below instead
	stripH       int
	tableW       int
	topH         int // sessions table and side column
	nowH         int // Now running panel, full width
}

const tileH = 3

func (m *model) dashLayout() dashLayout {
	var l dashLayout
	l.compactTiles = m.w < 112
	avail := m.h - 3 - tileH - 1
	l.tableW = m.w
	if m.w >= 120 {
		l.sideW = 38
		l.tableW = m.w - l.sideW - 1
	} else {
		l.stripH = 3 // heading naming the scope, then the two strips
	}
	avail -= l.stripH
	l.nowH = min(max(len(m.nowRunning()), 1)+2, 10)
	if l.nowH = min(l.nowH, avail-4); l.nowH < 3 {
		l.nowH = 0
	}
	// the table and side column take what their content needs, up to what is left; the side column is
	// sized from the whole range, so it does not jump as the cursor moves
	need := max(len(m.rows())+3, 7)
	if l.sideW > 0 {
		t := m.totals()
		need = max(need, len(roleUsage(t.byAgent))+2+len(t.byModel)+3)
	}
	l.topH = max(min(need, avail-l.nowH), 3)
	return l
}

// dashRows is how many display rows fit in the table.
func (m *model) dashRows() int { return max(1, m.dashLayout().topH-3) }

func (m *model) dashBody(p paint, w, h int) block {
	l := m.dashLayout()
	sc, shown := m.scope()
	t := sumTotals(sc.group) // the row under the cursor: the same sum as its table columns
	label := ""
	if shown {
		label = sc.label()
	}
	out := m.tiles(p, w, l.compactTiles)
	out = append(out, m.caption(p, w))
	table := m.sessionsTable(p, l.tableW, l.topH)
	if l.sideW == 0 {
		out = append(out, table...)
	} else {
		agentH := min(len(roleUsage(m.totals().byAgent))+2, max(l.topH-4, 3))
		modelH := l.topH - agentH
		side := m.agentMixBox(p, label, t, l.sideW, agentH)
		if modelH >= 3 {
			side = append(side, m.modelMixBox(p, label, t, l.sideW, modelH)...)
		}
		out = append(out, joinCols(p, l.topH, 1, []int{l.tableW, l.sideW}, table, side)...)
	}
	if l.nowH > 0 {
		out = append(out, m.nowRunningBox(p, w, l.nowH)...)
	}
	if l.stripH > 0 {
		head := p.t(" mix", m.th.Faint)
		if label != "" {
			head += p.t(" · ", m.th.Faint) + p.t(label, m.th.Text) + p.t(" · ", m.th.Faint) + p.t(m.money(t.cost, t.unpriced), m.th.Text)
		}
		out = append(out, fit(head, w, p))
		rows, _ := agentUsage(roleUsage(t.byAgent))
		out = append(out, m.scopeStrip(p, "agents", rows, t.cost, m.th.AgentColor, func(k string) string { return k }, w))
		rows, _ = sortedUsage(t.byModel)
		out = append(out, m.scopeStrip(p, "models", rows, t.cost, m.th.ModelColor, shortModel, w))
	}
	return out
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// caption says what the tiles total, since the mix panels follow the cursor instead.
func (m *model) caption(p paint, w int) string {
	n := len(m.visible())
	if n == 0 {
		return p.sp(w)
	}
	return fit(p.t(fmt.Sprintf(" ↑ totals of all %s shown · the mix panels show the selected row", plural(n, "session")), m.th.Faint), w, p)
}

// label names a row for the mix panels: its kind, its text, its suffix.
func (r treeRow) label() string {
	word := [...]string{kindRepo: "repo ", kindTask: "task ", kindSession: "session ", kindAll: ""}[r.kind]
	if r.suffix == "" {
		return word + r.name
	}
	return word + r.name + " " + r.suffix
}

// mixStrip is a one-line stacked bar with a legend, for the mixes when there is no side column.
func (m *model) mixStrip(p paint, label string, rows []kv, total float64, col func(string) color.Color, name func(string) string, w int) string {
	th := m.th
	var segs []seg
	var legend []string
	for i, r := range rows {
		c := col(r.k)
		segs = append(segs, seg{r.v, c})
		share := 0.0
		if total > 0 {
			share = r.v / total * 100
		}
		legend = append(legend, p.swatch(i, c)+p.t(" "+name(r.k)+" "+pct(share), th.Muted))
	}
	bw := min(max(w/4, 8), 24)
	line := p.sp(1) + p.t(cell(label, 7, false), th.Faint) + p.stack(bw, segs) + p.sp(2) + strings.Join(legend, p.sp(2))
	return fit(line, w, p)
}

// scopeStrip is mixStrip for the dashboard's scope: each legend entry also shows its money, and an entry
// that does not fit is left out whole rather than cut mid-number.
func (m *model) scopeStrip(p paint, label string, rows []kv, total float64, col func(string) color.Color, name func(string) string, w int) string {
	var segs []seg
	for _, r := range rows {
		segs = append(segs, seg{r.v, col(r.k)})
	}
	line := p.sp(1) + p.t(cell(label, 7, false), m.th.Faint) + p.stack(min(max(w/6, 6), 16), segs)
	var entries []string
	for i, r := range rows {
		share := 0.0
		if total > 0 {
			share = r.v / total * 100
		}
		entries = append(entries, p.sp(2)+p.swatch(i, col(r.k))+p.t(" "+name(r.k)+" "+m.money(r.v, r.unpriced)+" "+pct(share), m.th.Muted))
	}
	// keep the most entries that fit with the " +N" marker for the rest; the first is always shown
	k, used := 0, width(line)
	for k < len(entries) {
		marker := 0
		if more := len(entries) - k - 1; more > 0 {
			marker = len(fmt.Sprintf(" +%d", more))
		}
		if k > 0 && used+width(entries[k])+marker > w {
			break
		}
		used += width(entries[k])
		k++
	}
	line += strings.Join(entries[:k], "")
	if k < len(entries) {
		line += p.t(fmt.Sprintf(" +%d", len(entries)-k), m.th.Faint)
	}
	return fit(line, w, p)
}

// nowRunningBox lists the running dispatches of live sessions, with elapsed time ticking.
func (m *model) nowRunningBox(p paint, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	iw := w - 4
	items := m.nowRunning()
	var lines []string
	if len(items) == 0 {
		lines = []string{in.t("nothing running", th.Faint)}
	}
	agentW, projW, costW, tokW, durW := 16, 16, 9, 10, 7
	if iw < 70 {
		projW = 0
	}
	descW := iw - 2 - agentW - 1 - costW - 1 - tokW - 1 - durW - 1
	if projW > 0 {
		descW -= projW + 1
	}
	room := h - 2
	for i, it := range items {
		if i == room-1 && len(items) > room {
			lines = append(lines, in.t(fmt.Sprintf("+%d more running", len(items)-i), th.Faint))
			break
		}
		x := it.d
		line := in.t(spin(m.spinFrame()), th.Blue) + in.sp(1) + in.b(cell(x.AgentType, agentW, false), th.AgentColor(x.AgentType)) + in.sp(1)
		if projW > 0 {
			line += in.t(cell(it.s.Label(), projW, false), th.Purple) + in.sp(1)
		}
		line += in.t(cell(firstLine(x.Description), max(descW, 0), false), th.Text) + in.sp(1)
		line += in.t(cell(FmtDuration(m.now().Sub(*x.Start)), durW, true), th.Text) + in.sp(1)
		line += in.t(cell(m.money(x.Usage.Cost, x.Usage.Unpriced), costW, true), th.Text) + in.sp(1)
		line += in.t(cell(Human(float64(x.Usage.Total))+" tok", tokW, true), th.Muted)
		lines = append(lines, line)
	}
	title := "Now running"
	if len(items) > 0 {
		title += fmt.Sprintf(" %d", len(items))
	}
	return p.boxed(title, lines, w, h, th.Blue)
}

// ---- KPI tiles -------------------------------------------------------------------

type tile struct {
	label string
	value string
	sub   string // preformatted, may be a gauge
	color color.Color
}

func (m *model) tiles(p paint, w int, compact bool) block {
	th := m.th
	c := func(k string) float64 { return m.counter[k].Value() }
	t := m.totals()
	count := func(k string, warn color.Color) (string, color.Color) {
		v := int(c(k))
		col := th.Green
		if v > 0 {
			col = warn
		}
		return fmt.Sprintf("%d", v), col
	}
	sp := p.on(th.Surface)
	shareV := c("share") / 10
	gauge := func(tw int) string {
		return sp.bar(tw-4, shareV/100, th.Purple, true)
	}
	tok := Human(m.counter["tokens"].Cur) + " tok"
	var tiles []tile
	tiles = append(tiles, tile{"COST", m.money(t.cost, t.unpriced), tok, th.Blue})
	tiles = append(tiles, tile{"SUBAGENT SHARE", fmt.Sprintf("%.1f%%", shareV), "\x00gauge", th.Purple})
	tiles = append(tiles, tile{"DISPATCHES", fmt.Sprintf("%d", int(c("dispatches"))), avgDisp(t.dispatches, len(m.visible())), th.Teal})
	rv, rc := count("repeats", th.Yellow)
	lv, lc := count("loops", th.Yellow)
	wv, wc := count("warns", th.Red)
	rate := "-"
	rateCol := th.Muted
	if rr := c("recheck"); rr >= 0 {
		rate = fmt.Sprintf("%.0f%%", rr/10)
		rateCol = th.Green
		if rr > 0 {
			rateCol = th.Orange
		}
	}
	if compact {
		sig := p.on(th.Surface)
		val := sig.b("↻"+rv, rc) + sig.sp(1) + sig.b("↺"+lv, lc) + sig.sp(1) + sig.b("⚠"+wv, wc)
		tiles = append(tiles, tile{"SIGNALS", "\x00" + val, "repeat · loop · warn", th.Yellow})
	} else {
		tiles = append(tiles,
			tile{"REPEATS", rv, "re-dispatches", rc},
			tile{"REVIEW LOOPS", lv, "reviewer → coder", lc},
			tile{"RE-CHECK RATE", rate, "claims re-read", rateCol},
			tile{"MODEL WARNINGS", wv, "wrong model used", wc})
	}
	n := len(tiles)
	gap := 1
	tw := (w - gap*(n-1)) / n
	extra := w - gap*(n-1) - tw*n
	var cols []block
	var widths []int
	for i, tl := range tiles {
		x := tw
		if i < extra {
			x++
		}
		widths = append(widths, x)
		cols = append(cols, m.renderTile(p, tl, x, gauge))
	}
	return joinCols(p, tileH, gap, widths, cols...)
}

func (m *model) renderTile(p paint, tl tile, w int, gauge func(int) string) block {
	th := m.th
	sp := p.on(th.Surface)
	stripe := sp.t("▌", tl.color)
	if th.NoColor {
		stripe = "▌"
	}
	inner := w - 2
	value := tl.value
	var v string
	if strings.HasPrefix(value, "\x00") {
		v = value[1:]
	} else {
		v = sp.b(value, tl.color)
	}
	sub := tl.sub
	var s3 string
	switch {
	case sub == "\x00gauge":
		s3 = gauge(w)
	default:
		s3 = sp.t(sub, th.Muted)
	}
	lines := []string{
		sp.t(tl.label, th.Faint),
		v,
		s3,
	}
	out := make(block, tileH)
	for i, ln := range lines {
		out[i] = stripe + fit(sp.sp(1)+trunc(ln, inner-1), w-1, sp)
	}
	return out
}

func avgDisp(n, sessions int) string {
	if sessions == 0 {
		return "no sessions"
	}
	return fmt.Sprintf("%.1f per session", float64(n)/float64(sessions))
}

// ---- sessions table -----------------------------------------------------------------

func (m *model) sessionsTable(p paint, w, h int) block {
	th := m.th
	rows := m.rows()
	n := len(m.visible())
	title := fmt.Sprintf("Sessions %d", n)
	in := p.on(th.Surface)
	iw := w - 4 // borders + one pad each side
	if n == 0 {
		return m.emptyTable(p, w, h, title)
	}
	cols := m.tableCols(iw)
	var lines []string
	lines = append(lines, in.t(cols.header(m.timeLabel()), th.Faint))
	room := h - 3
	cur, _ := m.cursorRow(rows)
	for i := m.top; i < len(rows) && i < m.top+room; i++ {
		lines = append(lines, m.treeLine(rows[i], i == cur, cols, iw, in))
	}
	// pad rows so the scroll indicator always sits at the bottom
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	box := p.boxed(title, lines, w, h, th.Purple)
	if len(rows) > room {
		box = m.scrollMark(p, box, m.top, room, len(rows))
	}
	return box
}

// scrollMark paints a thin scroll thumb over the right border.
func (m *model) scrollMark(p paint, box block, top, rows, n int) block {
	if len(box) < 4 {
		return box
	}
	track := len(box) - 2
	thumb := max(1, track*rows/n)
	start := 0
	if n > rows {
		start = top * (track - thumb) / (n - rows)
	}
	for i := start; i < start+thumb && i < track; i++ {
		line := box[i+1]
		box[i+1] = ansi.Truncate(line, width(line)-1, "") + p.t("┃", m.th.Purple)
	}
	return box
}

func (m *model) emptyTable(p paint, w, h int, title string) block {
	th := m.th
	in := p.on(th.Surface)
	var lines []string
	if m.filter != "" {
		lines = []string{in.b("No sessions match “"+m.filter+"”", th.Text), in.t("esc clears the filter", th.Muted)}
	} else {
		lines = []string{
			in.b("No sessions in the last "+ranges[m.rangeIdx].label, th.Text),
			in.t("Nothing has been written under the projects directory in this range.", th.Muted),
			in.b("r", th.Purple) + in.t(" widens the range · new transcripts appear here live", th.Muted),
		}
	}
	inner := h - 2
	blk := make(block, inner)
	top := max(0, (inner-len(lines))/2)
	for i, l := range lines {
		pad := max(0, (w-4-width(l))/2)
		if top+i < inner {
			blk[top+i] = in.sp(pad) + l
		}
	}
	return p.boxed(title, blk, w, h, th.Purple)
}

type tableCols struct {
	name, start, cost, tok, split, dis   int
	showStart, sig, showTok, subPct, bad bool
}

const (
	sigW             = 3 + 1 + 4 + 1 + 4 + 1 + 4 // REP LOOP RCHK WARN
	minName, maxName = 14, 30
)

// tableCols adds the optional columns in a fixed order as the width allows (signals, time, TOKENS, SUB%, BAD),
// so a wider table never loses a column.
func (m *model) tableCols(iw int) tableCols {
	c := tableCols{cost: 9, tok: 7, split: 8, dis: 4, start: 11}
	avail := iw - (3 + 1 + c.cost + 1 + c.split + 1 + c.dis)
	want, malformed := minName, false
	for _, r := range m.rows() {
		want = max(want, 2*r.depth+width(r.name)+1+width(r.suffix))
		malformed = malformed || r.s != nil && r.s.Malformed > 0
	}
	want = min(want, maxName)
	add := func(on bool, w, keep int) bool {
		if !on || avail-w < keep {
			return false
		}
		avail -= w
		return true
	}
	c.sig = add(true, sigW+1, 10) // kept at the 60-column minimum, where an indented id is cut
	c.showStart = add(c.sig, c.start+1, minName)
	c.showTok = add(c.showStart, c.tok+1, want)
	c.subPct = add(c.showTok, 5, want)
	c.bad = add(c.subPct && malformed, 4, want)
	c.name = min(want, avail)
	slack := avail - c.name
	grow := min(slack, 8)
	c.split += grow
	c.name += min(slack-grow, maxName-c.name)
	return c
}

func (c tableCols) header(timeLabel string) string {
	s := "   " + cell("REPO / TASK", c.name, false) + " "
	if c.showStart {
		s += cell(timeLabel, c.start, false) + " "
	}
	s += cell("COST", c.cost, true) + " "
	if c.showTok {
		s += cell("TOKENS", c.tok, true) + " "
	}
	s += cell("MAIN│SUB", c.split, false)
	if c.subPct {
		s += " " + cell("SUB%", 4, true)
	}
	s += " " + cell("DISP", c.dis, true)
	if c.sig {
		s += " " + cell("REP", 3, true) + " " + cell("LOOP", 4, true) + " " + cell("RCHK", 4, true) + " " + cell("WARN", 4, true)
	}
	if c.bad {
		s += " " + cell("BAD", 3, true)
	}
	return s
}

// timeLabel names what the time column shows: it follows the sort.
func (m *model) timeLabel() string {
	if m.sortBy == sortRecent {
		return "ACTIVE"
	}
	return "STARTED"
}

// rowTime is the latest activity of a row's sessions when sorted by recent, else their earliest start.
func (m *model) rowTime(group []*data.Session) time.Time {
	var out time.Time
	for _, s := range group {
		t := m.activity(s)
		if m.sortBy != sortRecent {
			t = time.Time{}
			if s.Start != nil {
				t = *s.Start
			}
		}
		if t.IsZero() {
			continue
		}
		if out.IsZero() || (m.sortBy == sortRecent) == t.After(out) {
			out = t
		}
	}
	return out
}

// treeLine renders one row. Every row can be selected; only rows with a session flash.
// A group row shows a live dot when any of its sessions is live.
func (m *model) treeLine(r treeRow, selected bool, c tableCols, iw int, base paint) string {
	th := m.th
	bg := th.Surface
	if selected {
		bg = th.Raised
	}
	if r.s != nil {
		if until, ok := m.flash[r.s.ID]; ok {
			k := float64(until.Sub(m.now())) / float64(flashFor)
			if k > 0 {
				bg = mix(bg, th.Purple, 0.28*k)
			}
		}
	}
	q := base.on(bg)
	marker := q.sp(1)
	if selected {
		marker = q.b("▌", th.Purple)
		if th.NoColor {
			marker = ">"
		}
	}
	dot := q.sp(1)
	if r.s != nil {
		dot = q.t("·", th.Faint)
	}
	if slices.ContainsFunc(r.group, m.isLive) {
		dot = q.dot(th.Green, m.pulse())
	}
	lead := strings.Repeat("  ", r.depth) + r.name
	text := lead
	if r.suffix != "" {
		text += " " + r.suffix
	}
	text = cell(text, c.name, false)
	n := min(width(lead), c.name)
	head, tail := ansi.Truncate(text, n, ""), ansi.TruncateLeft(text, n, "")
	var name string
	switch {
	case r.kind == kindTask:
		name = q.b(head, th.Purple)
		if !selected {
			name = q.t(head, th.Purple)
		}
	case r.kind == kindRepo || r.kind == kindAll || selected:
		name = q.b(head, th.Text)
	default:
		name = q.t(head, th.Faint)
	}
	line := marker + dot + q.sp(1) + name + q.t(tail, th.Faint) + q.sp(1)
	if c.showStart {
		st := "-"
		if t := m.rowTime(r.group); !t.IsZero() {
			st = t.Local().Format("01-02 15:04")
		}
		line += q.t(cell(st, c.start, false), th.Muted) + q.sp(1)
	}
	t := sumTotals(r.group)
	tok := float64(t.tokens)
	if r.s != nil {
		if rc := m.rowTok[r.s.ID]; rc != nil {
			tok = rc.Value()
		}
	}
	line += q.t(cell(m.money(t.cost, t.unpriced), c.cost, true), th.Text) + q.sp(1)
	if c.showTok {
		line += q.t(cell(Human(tok), c.tok, true), th.Muted) + q.sp(1)
	}
	line += q.stack(c.split, []seg{{t.mainCost, th.Yellow}, {t.subCost, th.Purple}})
	if c.subPct {
		line += q.sp(1) + q.t(cell(share(t.subCost, t.cost), 4, true), th.Muted)
	}
	line += q.sp(1) + q.t(cell(fmt.Sprintf("%d", t.dispatches), c.dis, true), th.Muted)
	if c.sig {
		line += q.sp(1) + countCell(q, t.repeats, 3, th.Yellow, false) + q.sp(1) + countCell(q, t.loops, 4, th.Yellow, false)
		line += q.sp(1) + recheckCell(q, t) + q.sp(1) + countCell(q, t.warns, 4, th.Red, true)
	}
	if c.bad {
		line += q.sp(1) + countCell(q, t.malformed, 3, th.Faint, false)
	}
	return fit(line, iw+1, q)
}

// countCell is a right-aligned count in col, or a faint dot for zero.
func countCell(q paint, n, w int, col color.Color, bold bool) string {
	switch {
	case n == 0:
		return q.t(cell("·", w, true), q.th.Faint)
	case bold:
		return q.b(cell(fmt.Sprint(n), w, true), col)
	}
	return q.t(cell(fmt.Sprint(n), w, true), col)
}

// recheckCell is the share of explorer claims re-read, from the summed counts.
func recheckCell(q paint, t totals) string {
	th := q.th
	switch {
	case t.candidates == 0:
		return q.t(cell("·", 4, true), th.Faint)
	case t.rechecked == 0:
		return q.t(cell("0%", 4, true), th.Muted)
	}
	return q.t(cell(share(float64(t.rechecked), float64(t.candidates)), 4, true), th.Orange)
}

// share is n of d as a whole percent, from summed counts or money; "-" when d is 0.
func share(n, d float64) string {
	if d == 0 {
		return "-"
	}
	return pct(100 * n / d)
}

// ---- mix panels -----------------------------------------------------------------------

type kv struct {
	k        string
	v        float64 // cost
	unpriced int64   // tokens with no price
}

func usageRows(m map[string]data.Usage) ([]kv, float64) {
	var out []kv
	var total float64
	for k, u := range m {
		out = append(out, kv{k, u.Cost, u.Unpriced})
		total += u.Cost
	}
	return out, total
}

// sortedUsage lists a usage map by cost, largest first (models).
func sortedUsage(m map[string]data.Usage) ([]kv, float64) {
	out, total := usageRows(m)
	sort.Slice(out, func(i, j int) bool {
		if out[i].v != out[j].v {
			return out[i].v > out[j].v
		}
		return out[i].k < out[j].k
	})
	return out, total
}

// agentUsage lists a usage map by agent type in the fixed agent order.
func agentUsage(m map[string]data.Usage) ([]kv, float64) {
	out, total := usageRows(m)
	sort.Slice(out, func(i, j int) bool { return agentLess(out[i].k, out[j].k) })
	return out, total
}

// roleUsage adds each plugin agent (factory:coder) into its role (coder), for the mix panels.
func roleUsage(m map[string]data.Usage) map[string]data.Usage {
	out := map[string]data.Usage{}
	for k, u := range m {
		v := out[agentRole(k)]
		v.Add(u)
		out[agentRole(k)] = v
	}
	return out
}

// boxTitle puts the scope in a mix panel's title; no scope, no label.
func boxTitle(name, label string) string {
	if label == "" {
		return name
	}
	return name + " · " + label
}

func (m *model) agentMixBox(p paint, label string, t totals, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	rows, _ := agentUsage(roleUsage(t.byAgent))
	iw := w - 4
	nameW := 0
	for _, r := range rows {
		nameW = max(nameW, width(r.k))
	}
	nameW = min(nameW, 10)
	var lines []string
	for i, r := range rows {
		if i >= h-2 {
			break
		}
		col := th.AgentColor(r.k)
		share := 0.0
		if t.cost > 0 {
			share = r.v / t.cost
		}
		bw := max(4, iw-2-nameW-1-1-9-5)
		lines = append(lines, in.t("● ", col)+in.t(cell(r.k, nameW, false), th.Text)+in.sp(1)+
			in.bar(bw, share, col, false)+in.sp(1)+in.t(cell(m.money(r.v, r.unpriced), 9, true), th.Muted)+in.t(cell(pct(share*100), 5, true), th.Faint))
	}
	if len(rows) == 0 {
		lines = []string{in.t("no data yet", th.Faint)}
	}
	return p.boxed(boxTitle("Agent mix", label), lines, w, h, th.Yellow)
}

func (m *model) modelMixBox(p paint, label string, t totals, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	rows, _ := sortedUsage(t.byModel)
	total := t.cost
	iw := w - 4
	var segs []seg
	for _, r := range rows {
		segs = append(segs, seg{r.v, th.ModelColor(r.k)})
	}
	lines := []string{in.stack(iw, segs)}
	for i, r := range rows {
		share := 0.0
		if total > 0 {
			share = r.v / total * 100
		}
		amount := m.money(r.v, 0)
		if r.unpriced > 0 {
			amount = "unpriced"
		}
		lines = append(lines, in.swatch(i, th.ModelColor(r.k))+in.sp(1)+in.t(cell(shortModel(r.k), max(6, iw-16), false), th.Text)+
			in.t(cell(amount, 9, true), th.Muted)+in.t(cell(pct(share), 5, true), th.Faint))
	}
	if len(rows) == 0 {
		lines = []string{in.t("no data yet", th.Faint)}
	}
	return p.boxed(boxTitle("Model mix", label), lines, w, h, th.Teal)
}
