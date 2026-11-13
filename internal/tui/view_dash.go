package tui

import (
	"fmt"
	"image/color"
	"math"
	"sort"
	"strings"
	"time"

	"ccmon/internal/data"
)

type dashLayout struct {
	compactTiles bool
	sideW        int
	stripH       int
	tableW       int
	tableH       int
}

const tileH = 3

func (m *model) dashLayout() dashLayout {
	var l dashLayout
	bodyH := m.h - 3
	l.compactTiles = m.w < 112
	if m.w >= 124 {
		l.sideW = 38
	} else if m.h >= 34 {
		l.stripH = 8
	}
	l.tableW = m.w
	if l.sideW > 0 {
		l.tableW = m.w - l.sideW - 1
	}
	l.tableH = bodyH - tileH - 1
	if l.stripH > 0 {
		l.tableH -= l.stripH
	}
	return l
}

// dashRows is how many session rows fit in the table.
func (m *model) dashRows() int { return max(1, m.dashLayout().tableH-3) }

func (m *model) dashBody(p paint, w, h int) block {
	l := m.dashLayout()
	out := m.tiles(p, w, l.compactTiles)
	out = append(out, p.sp(w))
	t := m.totals()
	nVis := len(m.visible())
	// the table shrinks to its content; spare height goes to the activity chart
	tableH := l.tableH
	chartH := 0
	if nVis > 0 {
		want := nVis + 3
		if spare := l.tableH - want; spare >= 8 {
			tableH = want
			chartH = spare
		}
	}
	left := m.sessionsTable(p, l.tableW, tableH)
	if chartH > 0 {
		left = append(left, m.activityBox(p, l.tableW, chartH)...)
	}
	for len(left) < l.tableH && l.sideW > 0 {
		left = append(left, p.sp(l.tableW))
	}
	switch {
	case l.sideW > 0:
		agentH := min(len(t.byAgent)+2, max(l.tableH-6, 4))
		modelH := min(len(t.byModel)+4, max(l.tableH-agentH, 4))
		hotH := l.tableH - agentH - modelH
		if hotH < 5 {
			modelH += hotH
			hotH = 0
		}
		side := append(m.agentMixBox(p, t, l.sideW, agentH), m.modelMixBox(p, t, l.sideW, modelH)...)
		if hotH > 0 {
			side = append(side, m.hotspotsBox(p, l.sideW, hotH)...)
		}
		out = append(out, joinCols(p, l.tableH, 1, []int{l.tableW, l.sideW}, left, side)...)
	case l.stripH > 0:
		out = append(out, left...)
		for i := len(left); i < l.tableH; i++ {
			out = append(out, p.sp(w))
		}
		a := (w - 1) / 2
		out = append(out, joinCols(p, l.stripH, 1, []int{a, w - 1 - a},
			m.agentMixBox(p, t, a, l.stripH), m.modelMixBox(p, t, w-1-a, l.stripH))...)
	default:
		out = append(out, left...)
	}
	return out
}

// activityBox charts tokens per time bucket (by session start) as vertical bars.
func (m *model) activityBox(p paint, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	vis := m.visible()
	now := m.now()
	nb := 30
	var span time.Duration
	switch ranges[m.rangeIdx].since {
	case "24h":
		nb, span = 24, 24*time.Hour
	case "7d":
		nb, span = 14, 7*24*time.Hour
	case "30d":
		span = 30 * 24 * time.Hour
	default:
		earliest := now
		for _, s := range vis {
			if s.Start != nil && s.Start.Before(earliest) {
				earliest = *s.Start
			}
		}
		span = max(now.Sub(earliest), time.Hour)
	}
	iw := w - 4
	nb = max(1, min(nb, iw/2))
	bucket := span / time.Duration(nb)
	sums := make([]float64, nb)
	for _, s := range vis {
		if s.Start == nil {
			continue
		}
		i := int(s.Start.Sub(now.Add(-span)) / bucket)
		if i < 0 || i >= nb {
			if i >= nb {
				i = nb - 1
			} else {
				continue
			}
		}
		sums[i] += float64(s.Tokens.Total)
	}
	maxV := 0.0
	for _, v := range sums {
		maxV = max(maxV, v)
	}
	rows := h - 3 // borders + axis
	if rows < 2 {
		return p.boxed("Activity", []string{in.t("…", th.Faint)}, w, h, th.Orange)
	}
	cw := max(1, iw/nb)
	gapW := 0
	if cw >= 3 {
		gapW = 1
	}
	vparts := []string{" ", "▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}
	lines := make([]string, 0, rows+1)
	for r := rows - 1; r >= 0; r-- {
		var sb strings.Builder
		for i, v := range sums {
			eighths := 0
			if maxV > 0 {
				eighths = int(math.Sqrt(v/maxV)*float64(rows*8) + 0.5)
			}
			if v > 0 && eighths == 0 {
				eighths = 1
			}
			cellE := max(0, min(8, eighths-r*8))
			g := vparts[cellE]
			if th.NoColor && cellE > 0 {
				g = "█"
			}
			col := th.gradientAt(float64(i) / float64(max(1, nb-1)))
			if cellE == 0 {
				sb.WriteString(in.sp(cw))
			} else {
				sb.WriteString(in.t(strings.Repeat(g, cw-gapW), col) + in.sp(gapW))
			}
		}
		lines = append(lines, in.sp(max(0, (iw-nb*cw)/2))+sb.String())
	}
	left := now.Add(-span).Local().Format("01-02 15:04")
	if span <= 24*time.Hour {
		left = now.Add(-span).Local().Format("15:04")
	}
	axis := in.t(left, th.Faint)
	right := in.t("now · peak "+Human(maxV), th.Faint)
	lines = append(lines, axis+in.sp(max(1, iw-width(axis)-width(right)))+right)
	return p.boxed("Activity · tokens by session start (√ scale)", lines, w, h, th.Orange)
}

// hotspotsBox lists sessions that carry signals, worst first.
func (m *model) hotspotsBox(p paint, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	type hs struct {
		s     *data.Session
		score int
	}
	var all []hs
	for _, s := range m.visible() {
		if sc := s.WarnCount*3 + s.Repeats + s.ReviewLoops; sc > 0 {
			all = append(all, hs{s, sc})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].score > all[j].score })
	var lines []string
	if len(all) == 0 {
		lines = []string{in.t("✓ ", th.Green) + in.t("all clear: no repeats, loops or warnings", th.Muted)}
	}
	iw := w - 4
	for _, x := range all {
		if len(lines) >= h-2 {
			break
		}
		pills := m.signalPills(in, x.s, iw/2)
		name := cell(x.s.Project, max(6, iw-width(pills)-10), false)
		lines = append(lines, in.t(name, th.Text)+in.t(" "+short8(x.s.ID)+" ", th.Faint)+pills)
	}
	return p.boxed("Hot spots", lines, w, h, th.Red)
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
	vis := m.visible()
	title := fmt.Sprintf("Sessions %d", len(vis))
	in := p.on(th.Surface)
	iw := w - 4 // borders + one pad each side
	if len(vis) == 0 {
		return m.emptyTable(p, w, h, title)
	}
	cols := m.tableCols(iw)
	var lines []string
	lines = append(lines, in.t(cols.header(), th.Faint))
	rows := h - 3
	cur := m.cursor(vis)
	for i := m.top; i < len(vis) && i < m.top+rows; i++ {
		lines = append(lines, m.sessionRow(vis[i], i == cur, cols, iw, in))
	}
	// pad rows so the scroll indicator always sits at the bottom
	for len(lines) < h-2 {
		lines = append(lines, "")
	}
	box := p.boxed(title, lines, w, h, th.Purple)
	if len(vis) > rows {
		box = m.scrollMark(p, box, m.top, rows, len(vis))
	}
	return box
}

// scrollMark paints a thin scroll thumb into the right border.
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
	for i := 0; i < track; i++ {
		y := i + 1
		if i >= start && i < start+thumb {
			line := box[y]
			cut := trunc(line, width(line)-1)
			cut = strings.TrimSuffix(cut, "…")
			box[y] = cut + p.t("┃", m.th.Purple)
		}
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
	p                                   *model
	project, start, id, tok, split, dis int
	sig                                 int
	showID, showStart                   bool
}

func (m *model) tableCols(iw int) tableCols {
	c := tableCols{p: m}
	c.tok, c.split, c.dis = 7, 10, 5
	c.showStart = iw >= 60
	c.showID = iw >= 96
	c.start, c.id = 11, 8
	fixed := 2 + 1 + c.tok + 1 + c.split + 1 + c.dis // marker+dot, gaps
	if c.showStart {
		fixed += c.start + 1
	}
	if c.showID {
		fixed += c.id + 1
	}
	rest := iw - fixed - 1
	c.sig = 0
	if rest >= 30 {
		c.sig = min(28, rest-16)
	}
	c.project = rest - c.sig
	if c.sig > 0 {
		c.project--
	}
	c.project = max(8, min(c.project, 30))
	return c
}

func (c tableCols) header() string {
	s := "   " + cell("PROJECT", c.project, false) + " "
	if c.showID {
		s += cell("SESSION", c.id, false) + " "
	}
	if c.showStart {
		s += cell("STARTED", c.start, false) + " "
	}
	s += cell("TOKENS", c.tok, true) + " " + cell("MAIN│SUB", c.split, false) + " " + cell("DISP", c.dis, true)
	if c.sig > 0 {
		s += " " + "SIGNALS"
	}
	return s
}

func (m *model) sessionRow(s *data.Session, selected bool, c tableCols, iw int, base paint) string {
	th := m.th
	bg := th.Surface
	if selected {
		bg = th.Raised
	}
	if until, ok := m.flash[s.ID]; ok {
		k := float64(until.Sub(m.now())) / float64(flashFor)
		if k > 0 {
			bg = mix(bg, th.Purple, 0.28*k)
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
	dot := q.t("·", th.Faint)
	if m.isLive(s) {
		dot = q.dot(th.Green, m.pulse())
	}
	proj := cell(s.Project, c.project, false)
	name := q.t(proj, th.Text)
	if selected {
		name = q.b(proj, th.Text)
	}
	line := marker + dot + q.sp(1) + name + q.sp(1)
	if c.showID {
		line += q.t(cell(short8(s.ID), c.id, false), th.Faint) + q.sp(1)
	}
	if c.showStart {
		st := "-"
		if s.Start != nil {
			st = s.Start.Local().Format("01-02 15:04")
		}
		line += q.t(cell(st, c.start, false), th.Muted) + q.sp(1)
	}
	tok := float64(s.Tokens.Total)
	if rc := m.rowTok[s.ID]; rc != nil {
		tok = rc.Value()
	}
	line += q.t(cell(Human(tok), c.tok, true), th.Text) + q.sp(1)
	line += q.stack(c.split, []seg{{float64(s.Tokens.Main), th.Yellow}, {float64(s.Tokens.Subagents), th.Purple}}) + q.sp(1)
	line += q.t(cell(fmt.Sprintf("%d", s.DispatchCount), c.dis, true), th.Muted)
	if c.sig > 0 {
		line += q.sp(1) + m.signalPills(q, s, c.sig)
	}
	return fit(line, iw+1, q)
}

// signalPills lists the non-zero signals of a session as pills within w cells.
func (m *model) signalPills(q paint, s *data.Session, w int) string {
	th := m.th
	type pl struct {
		text string
		col  color.Color
	}
	var pls []pl
	if s.WarnCount > 0 {
		pls = append(pls, pl{fmt.Sprintf("WARN %d", s.WarnCount), th.Red})
	}
	if s.Repeats > 0 {
		pls = append(pls, pl{fmt.Sprintf("repeat %d", s.Repeats), th.Yellow})
	}
	if s.ReviewLoops > 0 {
		pls = append(pls, pl{fmt.Sprintf("loop %d", s.ReviewLoops), th.Yellow})
	}
	if s.Recheck.Rate != nil && *s.Recheck.Rate > 0 {
		pls = append(pls, pl{fmt.Sprintf("recheck %.0f%%", *s.Recheck.Rate*100), th.Orange})
	}
	if s.Malformed > 0 {
		pls = append(pls, pl{fmt.Sprintf("bad %d", s.Malformed), th.Faint})
	}
	var sb strings.Builder
	used := 0
	for _, x := range pls {
		pw := pillWidth(x.text)
		if q.th.NoColor {
			pw = width(x.text) + 2
		}
		if used+pw > w {
			break
		}
		sb.WriteString(q.pill(x.text, x.col))
		sb.WriteString(q.sp(1))
		used += pw + 1
	}
	if len(pls) == 0 {
		return q.t("·", th.Faint)
	}
	return sb.String()
}

// ---- mix panels -----------------------------------------------------------------------

type kv struct {
	k string
	v int64
}

func sortedUsage(m map[string]data.Usage) ([]kv, int64) {
	var out []kv
	var total int64
	for k, u := range m {
		out = append(out, kv{k, u.Total})
		total += u.Total
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].v != out[j].v {
			return out[i].v > out[j].v
		}
		return out[i].k < out[j].k
	})
	return out, total
}

func (m *model) agentMixBox(p paint, t totals, w, h int) block {
	th := m.th
	in := p.on(th.Surface)
	rows, total := sortedUsage(t.byAgent)
	iw := w - 4
	nameW := 0
	for _, r := range rows {
		nameW = max(nameW, width(r.k))
	}
	nameW = min(nameW, 14)
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
	lines := []string{in.stack(iw, segs), ""}
	for _, r := range rows {
		share := 0.0
		if total > 0 {
			share = float64(r.v) / float64(total) * 100
		}
		lines = append(lines, in.t("■ ", th.ModelColor(r.k))+in.t(cell(shortModel(r.k), max(6, iw-14), false), th.Text)+
			in.t(cell(Human(float64(r.v)), 7, true), th.Muted)+in.t(cell(pct(share), 5, true), th.Faint))
	}
	if len(rows) == 0 {
		lines = []string{in.t("no data yet", th.Faint)}
	}
	return p.boxed("Model mix", lines, w, h, th.Teal)
}

var _ = time.Second
