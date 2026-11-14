package tui

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"ccmon/internal/data"
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
	if m.w >= 100 {
		l.sideW = 32
		if m.w >= 120 {
			l.sideW = 38
		}
		l.tableW = m.w - l.sideW - 1
	} else {
		l.stripH = 2
	}
	avail -= l.stripH
	l.nowH = min(max(len(m.nowRunning()), 1)+2, 10)
	if l.nowH = min(l.nowH, avail-4); l.nowH < 3 {
		l.nowH = 0
	}
	// the table and side column take what their content needs, up to what is left
	need := max(len(m.rows())+3, 7)
	if l.sideW > 0 {
		t := m.totals()
		need = max(need, len(t.byAgent)+2+len(t.byModel)+3)
	}
	l.topH = max(min(need, avail-l.nowH), 3)
	return l
}

// dashRows is how many display rows fit in the table.
func (m *model) dashRows() int { return max(1, m.dashLayout().topH-3) }

func (m *model) dashBody(p paint, w, h int) block {
	l := m.dashLayout()
	t := m.totals()
	out := m.tiles(p, w, l.compactTiles)
	out = append(out, p.sp(w))
	table := m.sessionsTable(p, l.tableW, l.topH)
	if l.sideW == 0 {
		out = append(out, table...)
	} else {
		agentH := min(len(t.byAgent)+2, max(l.topH-4, 3))
		modelH := l.topH - agentH
		side := m.agentMixBox(p, t, l.sideW, agentH)
		if modelH >= 3 {
			side = append(side, m.modelMixBox(p, t, l.sideW, modelH)...)
		}
		out = append(out, joinCols(p, l.topH, 1, []int{l.tableW, l.sideW}, table, side)...)
	}
	if l.nowH > 0 {
		out = append(out, m.nowRunningBox(p, w, l.nowH)...)
	}
	if l.stripH > 0 {
		rows, total := agentUsage(t.byAgent)
		out = append(out, m.mixStrip(p, "agents", rows, total, m.th.AgentColor, func(k string) string { return k }, w))
		rows, total = sortedUsage(t.byModel)
		out = append(out, m.mixStrip(p, "models", rows, total, m.th.ModelColor, shortModel, w))
	}
	return out
}

// mixStrip is a one-line stacked bar with a legend, for the mixes when there is no side column.
func (m *model) mixStrip(p paint, label string, rows []kv, total int64, col func(string) color.Color, name func(string) string, w int) string {
	th := m.th
	var segs []seg
	var legend []string
	for i, r := range rows {
		c := col(r.k)
		segs = append(segs, seg{float64(r.v), c})
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total) * 100
		}
		legend = append(legend, p.swatch(i, c)+p.t(" "+name(r.k)+" "+pct(share), th.Muted))
	}
	bw := min(max(w/4, 8), 24)
	line := p.sp(1) + p.t(cell(label, 7, false), th.Faint) + p.stack(bw, segs) + p.sp(2) + strings.Join(legend, p.sp(2))
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
	agentW, projW, tokW, durW := 16, 16, 10, 7
	if iw < 70 {
		projW = 0
	}
	descW := iw - 2 - agentW - 1 - tokW - 1 - durW - 1
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
	tokV := Human(m.counter["tokens"].Cur)
	var tiles []tile
	tiles = append(tiles, tile{"TOKENS", tokV, fmt.Sprintf("%d sessions", len(m.visible())), th.Blue})
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
	name, start, tok, split, dis int
	sig                          int
	showStart                    bool
}

func (m *model) tableCols(iw int) tableCols {
	var c tableCols
	c.tok, c.split, c.dis = 7, 8, 4
	c.showStart = iw >= 60
	c.start = 11
	fixed := 2 + 1 + c.tok + 1 + c.split + 1 + c.dis // marker+dot, gaps
	if c.showStart {
		fixed += c.start + 1
	}
	rest := iw - fixed - 1
	c.sig = 0
	if rest >= 30 {
		c.sig = min(28, rest-15)
	}
	c.name = rest - c.sig
	if c.sig > 0 {
		c.name--
	}
	c.name = max(14, min(c.name, 30)) // room for a four-cell indent and an 8-character id
	return c
}

func (c tableCols) header(timeLabel string) string {
	s := "   " + cell("REPO / TASK", c.name, false) + " "
	if c.showStart {
		s += cell(timeLabel, c.start, false) + " "
	}
	s += cell("TOKENS", c.tok, true) + " " + cell("MAIN│SUB", c.split, false) + " " + cell("DISP", c.dis, true)
	if c.sig > 0 {
		s += " " + "SIGNALS"
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

// treeLine renders one repository, task or session row. Only session rows select, flash and show a live dot.
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
		if m.isLive(r.s) {
			dot = q.dot(th.Green, m.pulse())
		}
	}
	label := cell(strings.Repeat("  ", r.depth)+r.name, c.name, false)
	var name string
	switch {
	case r.s != nil && selected:
		name = q.b(label, th.Text)
	case r.s != nil:
		name = q.t(label, th.Faint)
	case r.task:
		name = q.t(label, th.Purple)
	default:
		name = q.b(label, th.Text)
	}
	line := marker + dot + q.sp(1) + name + q.sp(1)
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
	line += q.t(cell(Human(tok), c.tok, true), th.Text) + q.sp(1)
	line += q.stack(c.split, []seg{{float64(t.main), th.Yellow}, {float64(t.sub), th.Purple}}) + q.sp(1)
	line += q.t(cell(fmt.Sprintf("%d", t.dispatches), c.dis, true), th.Muted)
	if c.sig > 0 {
		line += q.sp(1) + m.signalPills(q, t, c.sig)
	}
	return fit(line, iw+1, q)
}

// signalPills lists the non-zero signals of a row's totals as pills within w cells.
func (m *model) signalPills(q paint, t totals, w int) string {
	th := m.th
	var pls []pill
	if t.warns > 0 {
		pls = append(pls, pill{fmt.Sprintf("WARN %d", t.warns), th.Red})
	}
	if t.repeats > 0 {
		pls = append(pls, pill{fmt.Sprintf("repeat %d", t.repeats), th.Yellow})
	}
	if t.loops > 0 {
		pls = append(pls, pill{fmt.Sprintf("loop %d", t.loops), th.Yellow})
	}
	if t.rechecked > 0 {
		pls = append(pls, pill{fmt.Sprintf("recheck %.0f%%", 100*float64(t.rechecked)/float64(t.candidates)), th.Orange})
	}
	if t.malformed > 0 {
		pls = append(pls, pill{fmt.Sprintf("bad %d", t.malformed), th.Faint})
	}
	if len(pls) == 0 {
		return q.t("·", th.Faint)
	}
	return m.flagPills(q, pls, w)
}

// ---- mix panels -----------------------------------------------------------------------

type kv struct {
	k string
	v int64
}

func usageRows(m map[string]data.Usage) ([]kv, int64) {
	var out []kv
	var total int64
	for k, u := range m {
		out = append(out, kv{k, u.Total})
		total += u.Total
	}
	return out, total
}

// sortedUsage lists a usage map by total, largest first (models).
func sortedUsage(m map[string]data.Usage) ([]kv, int64) {
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
func agentUsage(m map[string]data.Usage) ([]kv, int64) {
	out, total := usageRows(m)
	sort.Slice(out, func(i, j int) bool { return agentLess(out[i].k, out[j].k) })
	return out, total
}

func (m *model) agentMixBox(p paint, t totals, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	rows, total := agentUsage(t.byAgent)
	iw := w - 4
	nameW := 0
	for _, r := range rows {
		nameW = max(nameW, width(r.k))
	}
	nameW = min(nameW, 16)
	var lines []string
	for i, r := range rows {
		if i >= h-2 {
			break
		}
		col := th.AgentColor(r.k)
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total)
		}
		bw := max(4, iw-nameW-2-5-1)
		lines = append(lines, in.t("● ", col)+in.t(cell(r.k, nameW, false), th.Text)+in.sp(1)+
			in.bar(bw, share, col, false)+in.t(cell(pct(share*100), 5, true), th.Muted))
	}
	if len(rows) == 0 {
		lines = []string{in.t("no data yet", th.Faint)}
	}
	return p.boxed("Agent mix", lines, w, h, th.Yellow)
}

func (m *model) modelMixBox(p paint, t totals, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	rows, total := sortedUsage(t.byModel)
	iw := w - 4
	var segs []seg
	for _, r := range rows {
		segs = append(segs, seg{float64(r.v), th.ModelColor(r.k)})
	}
	lines := []string{in.stack(iw, segs)}
	for i, r := range rows {
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total) * 100
		}
		lines = append(lines, in.swatch(i, th.ModelColor(r.k))+in.sp(1)+in.t(cell(shortModel(r.k), max(6, iw-14), false), th.Text)+
			in.t(cell(Human(float64(r.v)), 7, true), th.Muted)+in.t(cell(pct(share), 5, true), th.Faint))
	}
	if len(rows) == 0 {
		lines = []string{in.t("no data yet", th.Faint)}
	}
	return p.boxed("Model mix", lines, w, h, th.Teal)
}
