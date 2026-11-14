package tui

import (
	"fmt"
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"isshoni/internal/data"
)

const chartH = chartRows + 5 // box border, the columns, the axis, the version labels, the caption, border

// kaizenBody is the kaizen view: the page's lines, scrolled when taller than the window.
func (m *model) kaizenBody(p paint, w, h int) block {
	return scrolled(p, m.kaizenLines(p, w, h), m.kz.top[m.kz.page], w, h)
}

func (m *model) kaizenLines(p paint, w, h int) block {
	th := m.th
	k := &m.kz
	switch {
	case k.doc == nil && k.err != "":
		return p.center(block{p.t("⚠ "+trunc(k.err, w-6), th.Red)}, w, h)
	case k.doc == nil:
		msg := p.t(spin(m.spinFrame())+" ", th.Purple) + p.t("reading the factory's logs…", th.Muted)
		return p.center(block{msg}, w, h)
	case !k.doc.StoreFound || len(k.doc.Tasks) == 0:
		msg := block{p.b("No factory tasks yet", th.Text), ""}
		for _, l := range wrapPlain("Tasks appear when a dispatcher starts leads with /factory:dispatch; this page measures what they log.", min(w-8, 60)) {
			msg = append(msg, p.t(l, th.Muted))
		}
		return p.center(msg, w, h)
	}
	var lines block
	if k.err != "" {
		lines = block{p.sp(1) + p.t("⚠ "+trunc(k.err, w-4), th.Red), ""}
	}
	switch k.page {
	case pageFlow:
		return append(lines, m.flowLines(p, w)...)
	case pageBoard:
		return append(lines, m.boardLines(p, w)...)
	}
	return append(lines, m.pulseLines(p, w)...)
}

// kaizenSubheader is the view's title, the page switcher and when the numbers were read.
func (m *model) kaizenSubheader(p paint) string {
	th := m.th
	k := &m.kz
	out := p.b("Kaizen", th.Text) + p.sp(2)
	for i, name := range kaizenPages {
		switch {
		case kaizenPage(i) != k.page:
			out += p.t(" "+name+" ", th.Muted)
		case th.NoColor:
			out += "[" + name + "]"
		default:
			out += p.on(mix(th.Bg, th.Purple, 0.3)).b(" "+name+" ", th.Purple)
		}
	}
	out += p.t("  ←→", th.Faint)
	if d := k.doc; d != nil && m.w >= 100 {
		out += p.t(fmt.Sprintf("  · %d landed of %d tasks in %s", d.Summary.Landed, d.Summary.Tasks, ranges[k.doneRange].label), th.Muted)
	}
	switch {
	case k.busy:
		out += p.t("  · "+spin(m.spinFrame())+" reading", th.Blue)
	case !k.doneAt.IsZero():
		out += p.t("  · as of "+k.doneAt.Local().Format("15:04:05"), th.Faint)
	}
	return out
}

// ---- shared pieces ----------------------------------------------------------------

// heading is a section title with a rule across the rest of the line.
func (m *model) heading(p paint, w int, title, note string) string {
	th := m.th
	out := p.sp(1) + p.b(title, th.Text)
	if note != "" {
		out += p.t("  "+note, th.Faint)
	}
	return fit(out+p.sp(1)+p.t(strings.Repeat("─", max(0, w-width(out)-2)), th.Border), w, p)
}

// legend lays painted items out in as many lines as w needs.
func legend(p paint, w int, items []string) block {
	var out block
	line := ""
	for _, it := range items {
		switch {
		case line == "":
			line = it
		case width(line)+3+width(it) <= w-2:
			line += p.sp(3) + it
		default:
			out = append(out, p.sp(1)+line)
			line = it
		}
	}
	if line != "" {
		out = append(out, p.sp(1)+line)
	}
	return out
}

// wrapCapped wraps s to w cells and keeps at most maxLines, the last ending in an ellipsis when cut.
func wrapCapped(s string, w, maxLines int) []string {
	ls := wrapPlain(s, w)
	if len(ls) > maxLines {
		ls = ls[:maxLines]
		ls[maxLines-1] = strings.TrimSuffix(trunc(ls[maxLines-1], w-1), "…") + "…"
	}
	return ls
}

func (m *model) kzMoney(x float64) string { return Money(x, m.kz.doc.Prices.Currency) }

func kzSecs(x float64) string { return FmtDuration(time.Duration(x * float64(time.Second))) }

// kzRounds shows review rounds, a fractional median as it is.
func kzRounds(x float64) string {
	if x == math.Round(x) {
		return strconv.Itoa(int(x))
	}
	return strconv.FormatFloat(x, 'f', 1, 64)
}

func kzVersion(v string) string {
	if v == "" {
		return "v?"
	}
	return "v" + v
}

// kzTime shows a timestamp as month-day and time in the local zone.
func kzTime(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "-"
	}
	return t.Local().Format("01-02 15:04")
}

// unpriced counts the tokens of landed tasks that had no price: their costs are a lower bound.
func unpriced(doc *data.Kaizen) int64 {
	var n int64
	for _, t := range landedTasks(doc) {
		if t.Cost != nil {
			n += t.Cost.UnpricedTokens
		}
	}
	return n
}

// ---- Pulse ------------------------------------------------------------------------

func (m *model) pulseLines(p paint, w int) block {
	d := m.kz.doc
	lines := m.kaizenTiles(p, w)
	lines = append(lines, "", m.rewardsLine(p, w), "")
	lines = append(lines, m.pulseCharts(p, w)...)
	lines = append(lines, "")
	lines = append(lines, m.defectLines(p, w)...)
	lines = append(lines, "")
	lines = append(lines, m.siteManagerLines(p, w)...)
	acted := 0
	for _, c := range []data.OutboxCounts{d.Outbox.Issues, d.Outbox.Decisions} {
		acted += c.Accepted + c.Rejected + c.Done
	}
	decisions := d.Outbox.Decisions
	th := m.th
	lines = append(lines, "", p.sp(1)+p.t(fmt.Sprintf("%d open issues · %d decisions · %d acted on  ", d.Outbox.Issues.Open,
		decisions.Open+decisions.Accepted+decisions.Rejected+decisions.Done, acted), th.Muted)+p.b("→ Board", th.Purple))
	for _, r := range d.Repos {
		if r.PRs == "ok" {
			continue
		}
		msg := fmt.Sprintf("pull requests unavailable for %s: %s; landed counts and times need gh", r.Repo, r.PRs)
		for i, l := range wrapPlain(msg, w-5) {
			lead := "  "
			if i == 0 {
				lead = "⚠ "
			}
			lines = append(lines, p.sp(1)+p.t(lead+l, th.Yellow))
		}
	}
	return lines
}

// trendSub is the line under a median tile: how the latest landed tasks compare with the
// earlier ones, or how many tasks the median covers. Lower is better for every measure.
func (m *model) trendSub(sp paint, pair func(*data.KaizenTrend) data.Pair, n int, long, tiny bool) string {
	th := m.th
	if tr := m.kz.doc.Summary.Trend; tr != nil {
		if pr := pair(tr); pr.Latest != nil && pr.Earlier != nil {
			arrow, pct, col := m.trendLook(*pr.Latest, *pr.Earlier)
			text := fmt.Sprintf("%s %d%% vs %d", arrow, pct, tr.EarlierN)
			if long {
				text = fmt.Sprintf("%s %d%% vs earlier %d", arrow, pct, tr.EarlierN)
			} else if tiny {
				text = fmt.Sprintf("%s %d%%", arrow, pct)
			}
			return "\x00" + sp.t(text, col)
		}
	}
	return "\x00" + sp.t(plural(n, "task"), th.Muted)
}

func (m *model) kaizenTiles(p paint, w int) block {
	th := m.th
	s := m.kz.doc.Summary
	c := func(k string) float64 { return m.counter[k].Value() }
	long := (w-4)/5-3 >= 18 // "↓ 35% vs earlier 8" fits
	tiny := (w-4)/5-3 < 10  // labels and lines shrink to fit
	sp := p.on(th.Surface)
	value := func(median *float64, show func() string) string {
		if median == nil {
			return "-"
		}
		return show()
	}
	cost := value(s.Cost.Median, func() string {
		v := m.kzMoney(c("kz-cost") / 100)
		if unpriced(m.kz.doc) > 0 {
			v += "*"
		}
		return v
	})
	rework := "rework -"
	if s.ReworkShare != nil {
		rework = fmt.Sprintf("rework %.0f%%", *s.ReworkShare*100)
		if long {
			rework += " of cost"
		} else if tiny {
			rework = fmt.Sprintf("↺ %.0f%%", *s.ReworkShare*100)
		}
	}
	first := "-"
	if s.FirstPass.Of > 0 {
		first = fmt.Sprintf("%d/%d", int(c("kz-yes")), s.FirstPass.Of)
	}
	flight, label := fmt.Sprintf("%d in flight", s.InFlight), func(long, short string) string {
		if tiny {
			return short
		}
		return long
	}
	if tiny {
		flight = fmt.Sprintf("%d open", s.InFlight)
	}
	tiles := []tile{
		{"LANDED", strconv.Itoa(int(c("kz-landed"))), flight, th.Green},
		{label("COST / TASK", "COST"), cost, m.trendSub(sp, func(t *data.KaizenTrend) data.Pair { return t.Cost }, s.Cost.N, long, tiny), th.Blue},
		{label("BRIEF → PR", "BRIEF"), value(s.BriefToPR.Median, func() string { return kzSecs(c("kz-brief")) }), m.trendSub(sp, func(t *data.KaizenTrend) data.Pair { return t.BriefToPR }, s.BriefToPR.N, long, tiny), th.Purple},
		{label("PR → MERGE", "MERGE"), value(s.PRToMerge.Median, func() string { return kzSecs(c("kz-merge")) }), m.trendSub(sp, func(t *data.KaizenTrend) data.Pair { return t.PRToMerge }, s.PRToMerge.N, long, tiny), th.Teal},
		{label("FIRST PASS", "1ST PASS"), first, rework, th.Pink},
	}
	return m.tileRow(p, w, tiles, nil)
}

// rewardsLine is the real rewards, or what the trend is waiting for. The rewards shimmer
// (the brand gradient sweeping across) for a few seconds after they are shown.
func (m *model) rewardsLine(p paint, w int) string {
	th := m.th
	s := m.kz.doc.Summary
	if rs := m.kz.doc.Rewards; len(rs) > 0 {
		var parts []string
		for _, r := range rs {
			parts = append(parts, "✦ "+r.Text+" · "+r.Slug)
		}
		text := strings.Join(parts, "   ")
		if !m.kaizenShimmer() {
			var sb strings.Builder
			for i, part := range parts {
				if i > 0 {
					sb.WriteString(p.sp(3))
				}
				sb.WriteString(p.b("✦", th.Yellow) + p.t(part[len("✦"):], th.Text))
			}
			return fit(p.sp(1)+sb.String(), w, p)
		}
		rs := []rune(text)
		pos := math.Mod(float64(m.now().Sub(m.kz.shownAt))/float64(time.Second), 1)*1.5 - 0.25 // one sweep a second
		var sb strings.Builder
		for i, r := range rs {
			u := float64(i) / float64(max(1, len(rs)-1))
			base := th.Text
			if r == '✦' {
				base = th.Yellow
			}
			k := math.Max(0, 1-math.Abs(u-pos)/0.2)
			sb.WriteString(p.b(string(r), mix(base, th.gradientAt(u), k)))
		}
		return fit(p.sp(1)+sb.String(), w, p)
	}
	need := s.TrendMin
	if need == 0 {
		need = 10
	}
	if tr := s.Trend; tr != nil {
		return fit(p.sp(1)+p.t(fmt.Sprintf("medians · latest %d vs earlier %d landed tasks", tr.LatestN, tr.EarlierN), th.Faint), w, p)
	}
	return fit(p.sp(1)+p.t(fmt.Sprintf("comparing latest with earlier starts at %d landed tasks (have %d)", need, s.Landed), th.Faint), w, p)
}

// kzSeries is one measure over the landed tasks that have it, in start order.
type kzSeries struct {
	title  string
	accent color.Color
	vals   []float64
	slugs  []string
	vers   []string
	median *float64
	label  func(float64) string
	good   func(float64) bool // colours a column green or orange; nil keeps the accent
	reward string             // the reward kind that marks the newest column
}

func (m *model) kaizenSeries() []kzSeries {
	th := m.th
	d := m.kz.doc
	landed := landedTasks(d)
	make1 := func(title string, accent color.Color, label func(float64) string, med data.Median, get func(data.KaizenTask) *float64, reward string) kzSeries {
		s := kzSeries{title: title, accent: accent, label: label, median: med.Median, reward: reward}
		for _, t := range landed {
			if v := get(t); v != nil {
				s.vals, s.slugs, s.vers = append(s.vals, *v), append(s.slugs, t.Slug), append(s.vers, t.Version)
			}
		}
		return s
	}
	rounds := make1("review rounds", th.Pink, kzRounds, d.Summary.Rounds,
		func(t data.KaizenTask) *float64 {
			if t.Rounds == nil {
				return nil
			}
			v := float64(*t.Rounds)
			return &v
		}, "")
	rounds.good = func(x float64) bool { return x <= 1 }
	return []kzSeries{
		make1("cost per task", th.Blue, func(x float64) string { return m.kzMoney(x) }, d.Summary.Cost, func(t data.KaizenTask) *float64 {
			if t.Cost == nil {
				return nil
			}
			return &t.Cost.Total
		}, "record_low_cost"),
		make1("brief → pull request", th.Purple, kzSecs, d.Summary.BriefToPR, func(t data.KaizenTask) *float64 { return t.BriefToPR }, "record_fast"),
		rounds,
		make1("pull request → merge", th.Teal, kzSecs, d.Summary.PRToMerge, func(t data.KaizenTask) *float64 { return t.PRToMerge }, ""),
	}
}

// pulseCharts places the four charts side by side while each gets 36 columns, else two by two, else stacked.
func (m *model) pulseCharts(p paint, w int) block {
	across := 1
	switch {
	case (w-3)/4 >= 36:
		across = 4
	case (w-1)/2 >= 36:
		across = 2
	}
	series := m.kaizenSeries()
	var out block
	for i := 0; i < len(series); i += across {
		var boxes []block
		var widths []int
		for j := i; j < i+across; j++ {
			bw := (w - (across - 1)) / across
			if j-i < w-(across-1)-bw*across {
				bw++
			}
			widths = append(widths, bw)
			boxes = append(boxes, m.chartBox(p, series[j], bw))
		}
		out = append(out, joinCols(p, chartH, 1, widths, boxes...)...)
	}
	return out
}

// chartBox is one chart in a titled box: the columns, an axis marking where the plugin version
// changed with the versions beneath it, and the latest and median values.
func (m *model) chartBox(p paint, s kzSeries, bw int) block {
	th := m.th
	in := p.on(th.Surface)
	iw := bw - 4
	if len(s.vals) < 2 {
		lines := []string{in.t(plural(len(s.vals), "landed task")+": charts start at 2", th.Muted)}
		if len(s.vals) == 1 {
			lines = append(lines, in.t("the one value: ", th.Faint)+in.b(s.label(s.vals[0]), s.accent))
		}
		return p.boxed(s.title, lines, bw, chartH, s.accent)
	}
	star := false
	for _, r := range m.kz.doc.Rewards {
		star = star || (s.reward != "" && r.Kind == s.reward && r.Slug == s.slugs[len(s.slugs)-1])
	}
	o := chartOpts{vals: s.vals, label: s.label, star: star, grow: m.kaizenGrow(), hasMedian: s.median != nil,
		pick: func(i int) color.Color {
			switch {
			case s.good == nil:
				return s.accent
			case s.good(s.vals[i]):
				return th.Green
			}
			return th.Orange
		}}
	if s.median != nil {
		o.median = *s.median
	}
	ch := in.chart(o, iw)
	lines := slices.Clone(ch.lines)
	axis, labels := m.versionAxis(in, s.vers, ch, iw)
	lines = append(lines, axis, labels)
	caption := in.t("latest ", th.Faint) + in.b(s.label(s.vals[len(s.vals)-1]), th.Text)
	if s.median != nil {
		caption += in.t(" · median ", th.Faint) + in.t(s.label(*s.median), th.Muted)
	}
	lines = append(lines, caption)
	title := s.title
	if ch.first > 0 {
		title += fmt.Sprintf(" · last %d/%d", len(s.vals)-ch.first, len(s.vals))
	}
	return p.boxed(title, lines, bw, chartH, s.accent)
}

// versionAxis is a rule under the columns with a ▲ under the first task of each plugin version
// after the first shown, and the version labels beneath it: the first at the left.
func (m *model) versionAxis(in paint, vers []string, ch chartOut, w int) (axis, labels string) {
	th := m.th
	type mark struct {
		x     int
		label string
	}
	marks := []mark{{ch.gutter, kzVersion(vers[ch.first])}}
	rule := []rune(strings.Repeat("─", w))
	for i := ch.first + 1; i < len(vers); i++ {
		if vers[i] != vers[i-1] {
			x := ch.gutter + (i-ch.first)*ch.stride
			rule[x] = '▲'
			marks = append(marks, mark{x, kzVersion(vers[i])})
		}
	}
	var ax, lb strings.Builder
	ax.WriteString(in.sp(ch.gutter))
	for i := ch.gutter; i < w; {
		j := i + 1
		for j < w && (rule[j] == '▲') == (rule[i] == '▲') {
			j++
		}
		col := th.Border
		if rule[i] == '▲' {
			col = th.Purple
		}
		ax.WriteString(in.t(string(rule[i:j]), col))
		i = j
	}
	at := 0
	for i, mk := range marks {
		limit := w
		if i+1 < len(marks) {
			limit = marks[i+1].x - 1
		}
		x, label := mk.x, mk.label
		if i == len(marks)-1 {
			x = min(x, w-width(label))
		} else {
			label = trunc(label, limit-x) // a cramped label is cut, never dropped
		}
		if x < at || x < 0 || label == "" {
			continue // no room: the marker still shows where the version changed
		}
		lb.WriteString(in.sp(x - at))
		lb.WriteString(in.t(label, th.Purple))
		at = x + width(label)
	}
	return ax.String(), lb.String()
}

// defectParts are the defect catchers with their colours and counts: ci and human are escapes.
func (m *model) defectParts() (names []string, cols []color.Color, vals []int) {
	th := m.th
	df := m.kz.doc.Summary.Defects
	names = []string{"tests", "reviewer", "lead", "ci", "human"}
	cols = []color.Color{th.Teal, th.AgentColor("reviewer"), th.AgentColor("main"), th.Orange, th.Red}
	vals = []int{df.Tests, df.Reviewer, df.Lead, df.CI, df.Human}
	if df.Other > 0 {
		names, cols, vals = append(names, "other"), append(cols, th.Muted), append(vals, df.Other)
	}
	return
}

func (m *model) defectLines(p paint, w int) block {
	th := m.th
	lines := block{m.heading(p, w, "defects caught by", "")}
	retros := false
	for _, t := range m.kz.doc.Tasks {
		retros = retros || t.Defects != nil
	}
	if !retros {
		return append(lines, p.sp(1)+p.t("no retrospectives yet", th.Faint))
	}
	names, cols, vals := m.defectParts()
	var segs []seg
	var items []string
	for i, n := range names {
		segs = append(segs, seg{float64(vals[i]), cols[i]})
		items = append(items, p.swatch(i, cols[i])+p.t(" "+n+" ", th.Muted)+p.b(strconv.Itoa(vals[i]), th.Text))
	}
	lines = append(lines, p.sp(1)+p.stack(w-2, segs))
	lines = append(lines, legend(p, w, items)...)
	df := m.kz.doc.Summary.Defects
	if df.CI+df.Human == 0 {
		lines = append(lines, p.sp(1)+p.t("none escaped to CI or the human", th.Green))
	}
	return lines
}

// ---- Flow -------------------------------------------------------------------------

func (m *model) flowLines(p paint, w int) block {
	th := m.th
	d := m.kz.doc
	s := d.Summary
	lines := block{m.heading(p, w, "where the money went", "landed tasks")}
	roles := []struct {
		name string
		val  float64
		col  color.Color
	}{
		{"lead", s.ByRole.Lead, th.AgentColor("main")}, {"coder", s.ByRole.Coder, th.AgentColor("coder")},
		{"reviewer", s.ByRole.Reviewer, th.AgentColor("reviewer")}, {"explorer", s.ByRole.Explorer, th.AgentColor("explorer")},
		{"other", s.ByRole.Other, th.Muted},
	}
	total := 0.0
	for _, r := range roles {
		total += r.val
	}
	var segs []seg
	var items []string
	for i, r := range roles {
		segs = append(segs, seg{r.val, r.col})
		if r.val <= 0 {
			continue
		}
		items = append(items, p.swatch(i, r.col)+p.t(" "+r.name+" ", th.Muted)+p.b(m.kzMoney(r.val), th.Text)+p.t(fmt.Sprintf(" %.0f%%", r.val/total*100), th.Faint))
	}
	if total <= 0 {
		lines = append(lines, p.sp(1)+p.t("no cost recorded for landed tasks yet", th.Faint))
	} else {
		lines = append(lines, p.sp(1)+p.stack(w-2, segs))
		lines = append(lines, legend(p, w, items)...)
		rework := p.b("-", th.Muted)
		if s.ReworkShare != nil {
			rework = p.b(fmt.Sprintf("%.0f%%", *s.ReworkShare*100), th.Text)
		}
		lines = append(lines, p.sp(1)+p.t("rework ", th.Muted)+rework+p.t(" (subagent runs after the first review)", th.Faint))
		if n := unpriced(d); n > 0 {
			lines = append(lines, p.sp(1)+p.t("* "+Human(float64(n))+" tokens had no price: costs are a lower bound", th.Yellow))
		}
	}
	lines = append(lines, "", m.heading(p, w, "where the time went", "brief to merge, landed tasks"))
	phases := []struct {
		name string
		val  float64
		col  color.Color
	}{{"building", s.Time.Building, th.Blue}, {"stopped", s.Time.Stopped, th.Orange}, {"waiting for merge", s.Time.MergeWait, th.Purple}}
	total = 0
	for _, ph := range phases {
		total += ph.val
	}
	if total <= 0 {
		lines = append(lines, p.sp(1)+p.t("no landed tasks yet", th.Faint))
	} else {
		segs, items = nil, nil
		for i, ph := range phases {
			segs = append(segs, seg{ph.val, ph.col})
			items = append(items, p.swatch(i, ph.col)+p.t(" "+ph.name+" ", th.Muted)+p.b(kzSecs(ph.val), th.Text)+p.t(fmt.Sprintf(" %.0f%%", ph.val/total*100), th.Faint))
		}
		lines = append(lines, p.sp(1)+p.stack(w-2, segs))
		lines = append(lines, legend(p, w, items)...)
	}
	lines = append(lines, "", m.heading(p, w, "tasks", "newest first"))
	lines = append(lines, m.taskTable(p, w)...)
	lines = append(lines, "", m.heading(p, w, "stops", "the line stopped for the human"))
	lines = append(lines, m.stopLines(p, w)...)
	lines = append(lines, "", m.heading(p, w, "what slowed tasks", "from retrospectives"))
	lines = append(lines, m.slowedLines(p, w)...)
	return lines
}

// kzStateLook is the glyph, label and colour of a task's state: landed, else its declared
// state with the factory view's phase look.
func (m *model) kzStateLook(t data.KaizenTask) (glyph, label string, c color.Color) {
	th := m.th
	switch {
	case t.Landed:
		return "✓", "landed", th.Green
	case t.State == "working":
		return "●", t.State, th.Blue
	case t.State == "blocked":
		return "▲", t.State, th.Orange
	case t.State == "handed-over":
		return "◆", t.State, th.Yellow
	case t.State == "acknowledged" || t.State == "dismissed":
		return "·", t.State, th.Faint
	case t.State == "":
		return "", "-", th.Faint
	}
	return "·", t.State, th.Muted
}

// kzCols are the columns of the task table in display order; the first dropped when the
// terminal is narrow is the last in kzDrop.
var kzCols = []string{"SLUG", "VER", "STATE", "BUILD", "MERGE", "COST", "ROLES", "REV", "REWORK", "STOPS"}
var kzDrop = []string{"ROLES", "VER", "REWORK", "MERGE"}

const kzRolesW = 10

func (m *model) taskTable(p paint, w int) block {
	th := m.th
	d := m.kz.doc
	tasks := slices.Clone(d.Tasks)
	slices.Reverse(tasks)
	text := func(t data.KaizenTask, col string) (string, color.Color) {
		opt := func(v *float64, f func(float64) string) (string, color.Color) {
			if v == nil {
				return "-", th.Faint
			}
			return f(*v), th.Text
		}
		switch col {
		case "SLUG":
			return t.Slug, th.Text
		case "VER":
			return kzVersion(t.Version), th.Purple
		case "STATE":
			g, l, c := m.kzStateLook(t)
			return strings.TrimLeft(g+" "+l, " "), c
		case "BUILD":
			return opt(t.BriefToPR, kzSecs)
		case "MERGE":
			return opt(t.PRToMerge, kzSecs)
		case "COST":
			if t.Cost == nil {
				return "-", th.Faint
			}
			return m.kzMoney(t.Cost.Total), th.Text
		case "REV":
			switch {
			case t.Rounds == nil:
				return "-", th.Faint
			case *t.Rounds == 1:
				return "1", th.Green
			}
			return strconv.Itoa(*t.Rounds), th.Text
		case "REWORK":
			if t.Cost == nil || t.Cost.Total <= 0 {
				return "-", th.Faint
			}
			return fmt.Sprintf("%.0f%%", t.Cost.Rework/t.Cost.Total*100), th.Muted
		case "STOPS":
			if t.Stops > 0 {
				return strconv.Itoa(t.Stops), th.Orange
			}
			return "0", th.Faint
		}
		return "", th.Text
	}
	widths := map[string]int{"ROLES": kzRolesW}
	for _, c := range kzCols {
		widths[c] = max(widths[c], width(c))
		for _, t := range tasks {
			s, _ := text(t, c)
			widths[c] = max(widths[c], width(s))
		}
	}
	slugMax := min(widths["SLUG"], 28)
	var cols []string
	for step := 0; ; step++ {
		cols = slices.DeleteFunc(slices.Clone(kzCols), func(c string) bool { return slices.Index(kzDrop, c) >= 0 && slices.Index(kzDrop, c) < step })
		used := 2
		for _, c := range cols[1:] {
			used += widths[c] + 2
		}
		if free := w - used; free >= min(slugMax, 16) || step == len(kzDrop) {
			widths["SLUG"] = max(1, min(slugMax, free))
			break
		}
	}
	right := map[string]bool{"BUILD": true, "MERGE": true, "COST": true, "REV": true, "REWORK": true, "STOPS": true}
	row := func(cell func(c string) string) string {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = cell(c)
		}
		return fit(p.sp(2)+strings.Join(parts, p.sp(2)), w, p)
	}
	head := row(func(c string) string { return p.t(cell(c, widths[c], right[c]), th.Faint) })
	repos := map[string]bool{}
	for _, t := range tasks {
		repos[t.Repo] = true
	}
	var out block
	emit := func(group []data.KaizenTask) {
		out = append(out, head)
		for _, t := range group {
			out = append(out, row(func(c string) string {
				if c == "ROLES" {
					if t.Cost == nil || t.Cost.Total <= 0 {
						return p.t(cell("-", kzRolesW, false), th.Faint)
					}
					r := t.Cost.ByRole
					return p.stack(kzRolesW, []seg{{r.Lead, th.AgentColor("main")}, {r.Coder, th.AgentColor("coder")}, {r.Reviewer, th.AgentColor("reviewer")}, {r.Explorer, th.AgentColor("explorer")}, {r.Other, th.Muted}})
				}
				s, col := text(t, c)
				return p.t(cell(s, widths[c], right[c]), col)
			}))
		}
	}
	if len(repos) <= 1 {
		emit(tasks)
		return out
	}
	var order []string
	for _, r := range d.Repos {
		order = append(order, r.Repo)
	}
	for _, t := range tasks {
		if !slices.Contains(order, t.Repo) {
			order = append(order, t.Repo)
		}
	}
	for _, r := range order {
		var group []data.KaizenTask
		for _, t := range tasks {
			if t.Repo == r {
				group = append(group, t)
			}
		}
		if len(group) == 0 {
			continue
		}
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, fit(p.sp(1)+p.b(r, th.Text)+p.t(fmt.Sprintf("  %d tasks", len(group)), th.Faint), w, p))
		emit(group)
	}
	return out
}

// stopColor is the colour of a stop's kind.
func (m *model) stopColor(kind string) color.Color {
	th := m.th
	switch kind {
	case "QUESTION":
		return th.Blue
	case "BLOCKED":
		return th.Orange
	case "OVERTIME":
		return th.Red
	}
	return th.Yellow
}

func (m *model) stopLines(p paint, w int) block {
	th := m.th
	stops := slices.Clone(m.kz.doc.Stops)
	slices.Reverse(stops)
	if len(stops) == 0 {
		return block{p.sp(1) + p.t("nothing stopped the line", th.Green)}
	}
	var out block
	slugW := 0
	for _, s := range stops {
		slugW = max(slugW, width(s.Slug))
	}
	slugW = min(slugW, 22)
	pill := pillWidth("OVERTIME")
	for _, s := range stops {
		waited, wcol := "-", th.Faint
		switch {
		case s.WaitedS != nil:
			waited, wcol = kzSecs(*s.WaitedS), th.Text
		case s.Kind != "DENIED":
			waited, wcol = "waiting", th.Orange
		}
		why, wcol2 := s.Why, th.Muted
		if why == "" {
			why, wcol2 = "reason not recorded", th.Faint
		}
		line := p.sp(1) + p.t(cell(kzTime(s.TS), 11, false), th.Muted) + p.sp(2) + p.t(cell(s.Slug, slugW, false), th.Text) + p.sp(2) +
			p.pill(s.Kind, m.stopColor(s.Kind)) + p.sp(1+pill-pillWidth(s.Kind)) + p.t(cell(waited, 7, true), wcol) + p.sp(2)
		// the reason in full, wrapped beside the row; under it when the row leaves no room
		indent := width(line)
		if w-indent-1 < 20 {
			out = append(out, fit(line, w, p))
			line, indent = p.sp(3), 3
		}
		for i, l := range wrapPlain(why, max(1, w-indent-1)) {
			if i > 0 {
				line = p.sp(indent)
			}
			out = append(out, fit(line+p.t(l, wcol2), w, p))
		}
	}
	return out
}

// siteManagerLines is the site manager's record: how often the human acted on what it
// escalated (precision) and how much of what needed the human it escalated (recall).
func (m *model) siteManagerLines(p paint, w int) block {
	th := m.th
	s := m.kz.doc.SiteManager
	lines := block{m.heading(p, w, "site manager", "what it escalated to the human, and what came of it")}
	if s.Escalated+s.Suppressed+s.Missed == 0 {
		for _, l := range wrapPlain("nothing escalated, suppressed or missed yet · precision and recall start with the first outcome (factory firing <id> acted|ignored)", w-2) {
			lines = append(lines, p.sp(1)+p.t(l, th.Faint))
		}
		return lines
	}
	ratio := func(name string, v *float64, of, missing string) string {
		if v == nil {
			return p.t(name+" ", th.Muted) + p.b("-", th.Faint) + p.t("  "+missing, th.Faint)
		}
		return p.t(name+" ", th.Muted) + p.b(pct(*v*100), th.Text) + p.t("  "+of, th.Muted)
	}
	lines = append(lines, legend(p, w, []string{
		ratio("precision", s.Precision, fmt.Sprintf("%d acted on of %d with an outcome", s.Acted, s.Acted+s.Ignored), "no outcome recorded yet"),
		ratio("recall", s.Recall, fmt.Sprintf("%d acted on of %d that needed the human", s.Acted, s.Acted+s.Missed), "nothing acted on or missed yet"),
	})...)
	count := func(n int, label string, col color.Color) string {
		if n == 0 {
			col = th.Muted
		}
		return p.b(strconv.Itoa(n), col) + p.t(" "+label, th.Muted)
	}
	missed := count(s.Missed, "missed", th.Red)
	if s.Missed > 0 {
		missed += p.t(fmt.Sprintf(" (%d suppressed first, %d never fired)", s.MissedSuppressed, s.MissedNeverFired), th.Muted)
	}
	return append(lines, legend(p, w, []string{
		count(s.Escalated, "escalated", th.Text), count(s.Acted, "acted on", th.Green), count(s.Ignored, "ignored", th.Orange),
		count(s.NoOutcome, "no outcome yet", th.Text), count(s.Suppressed, "suppressed", th.Text), missed,
	})...)
}

func (m *model) slowedLines(p paint, w int) block {
	th := m.th
	slow := slices.Clone(m.kz.doc.Slowed)
	slices.SortStableFunc(slow, func(a, b data.KaizenSlow) int { return strings.Compare(b.TS, a.TS) })
	if len(slow) == 0 {
		return block{p.sp(1) + p.t("nothing recorded yet", th.Faint)}
	}
	var out block
	for _, s := range slow {
		out = append(out, m.entryRows(p, w, "", s.Slug, s.Text, 1<<20, th.Purple)...)
	}
	return out
}

// entryRows is one record: an optional role, the slug, and the text wrapped to maxLines
// beside them, every later line indented under the text.
func (m *model) entryRows(p paint, w int, role, slug, text string, maxLines int, slugCol color.Color) block {
	th := m.th
	lead := p.sp(1)
	if role != "" && w >= 70 {
		agent := role
		if role == "lead" {
			agent = "main" // the lead is the main session everywhere else
		}
		lead += p.b(cell(role, 9, false), th.AgentColor(agent)) + p.sp(1)
	}
	slugW := min(18, max(8, (w-width(lead))/4))
	lead += p.t(cell(slug, slugW, false), slugCol) + p.sp(2)
	tw := max(10, w-width(lead)-1)
	var out block
	for i, l := range wrapCapped(text, tw, maxLines) {
		if i == 0 {
			out = append(out, fit(lead+p.t(l, th.Text), w, p))
			continue
		}
		out = append(out, fit(p.sp(width(lead))+p.t(l, th.Text), w, p))
	}
	return out
}

// ---- Board ------------------------------------------------------------------------

func (m *model) boardLines(p paint, w int) block {
	th := m.th
	ob := m.kz.doc.Outbox
	counts := func(label string, c data.OutboxCounts) string {
		num := func(n int, name string, col color.Color) string {
			if n == 0 {
				col = th.Faint
			}
			return p.b(strconv.Itoa(n), col) + p.t(" "+name, th.Faint)
		}
		return p.sp(1) + p.t(cell(label, 10, false), th.Muted) + num(c.Open, "open", th.Text) + p.t(" · ", th.Faint) +
			num(c.Accepted, "accepted", th.Blue) + p.t(" · ", th.Faint) + num(c.Done, "done", th.Green) + p.t(" · ", th.Faint) + num(c.Rejected, "rejected", th.Muted)
	}
	lines := block{counts("issues", ob.Issues), counts("decisions", ob.Decisions), ""}

	lines = append(lines, m.heading(p, w, "open issues by topic", "grouped by keyword"))
	if len(ob.Topics) == 0 {
		lines = append(lines, p.sp(1)+p.t("no open issues", th.Green))
	}
	most := 1
	for _, t := range ob.Topics {
		most = max(most, t.Open)
	}
	for i, t := range ob.Topics {
		if i > 0 {
			lines = append(lines, "")
		}
		bw := min(30, max(8, w-30))
		lines = append(lines, fit(p.sp(1)+p.b(cell(t.Topic, 12, false), th.Purple)+p.b(cell(strconv.Itoa(t.Open), 3, false), th.Text)+p.sp(1)+p.bar(bw, float64(t.Open)/float64(most), th.Purple, false), w, p))
		shown := 0
		for _, e := range ob.Entries {
			if e.Kind != "issue" || e.Status != "open" || e.Topic != t.Topic {
				continue
			}
			if shown == 3 {
				lines = append(lines, p.sp(3)+p.t(fmt.Sprintf("+%d more", t.Open-3), th.Faint))
				break
			}
			lines = append(lines, m.entryRows(p, w, e.Role, e.Slug, e.Text, 2, th.Muted)...)
			shown++
		}
	}

	lines = append(lines, "", m.heading(p, w, "acted on", "accepted, done or rejected"))
	acted := 0
	for _, e := range ob.Entries {
		if e.Status == "open" {
			continue
		}
		acted++
		col := th.Muted
		switch e.Status {
		case "accepted":
			col = th.Blue
		case "done":
			col = th.Green
		}
		line := p.sp(1) + p.pill(e.Status, col) + p.sp(1+pillWidth("accepted")-pillWidth(e.Status)) + p.t(cell(e.Kind, 9, false), th.Faint)
		if e.PR != "" {
			line += p.t(prLabel(e.PR), th.Teal) + p.sp(1)
		}
		lines = append(lines, fit(line+p.t(trunc(strings.TrimSpace(strings.TrimPrefix(e.Text, "DECISION:")), max(0, w-width(line)-1)), th.Text), w, p))
	}
	if acted == 0 {
		lines = append(lines, p.sp(1)+p.t("nothing acted on yet: ", th.Faint)+p.t("`factory feedback set <id> accepted|done|rejected`", th.Muted))
	}

	lines = append(lines, "", m.heading(p, w, "decisions", "the newest 10"))
	n := 0
	for _, e := range ob.Entries {
		if e.Kind != "decision" {
			continue
		}
		if n == 10 {
			break
		}
		n++
		lines = append(lines, m.entryRows(p, w, "", e.Slug, strings.TrimSpace(strings.TrimPrefix(e.Text, "DECISION:")), 2, th.Muted)...)
	}
	if n == 0 {
		lines = append(lines, p.sp(1)+p.t("no decisions recorded", th.Faint))
	}
	return lines
}

// prLabel shortens a pull request link to #N.
func prLabel(pr string) string {
	if i := strings.LastIndex(pr, "/"); i >= 0 && strings.Contains(pr, "://") {
		return "#" + pr[i+1:]
	}
	return pr
}
