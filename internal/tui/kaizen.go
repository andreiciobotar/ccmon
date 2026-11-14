package tui

import (
	"image/color"
	"math"
	"reflect"
	"time"

	tea "charm.land/bubbletea/v2"

	"isshoni/internal/data"
)

const (
	kaizenFresh = 30 * time.Second       // entering the view within this of a result starts no load
	growFor     = 600 * time.Millisecond // the chart columns grow from zero this long
	shimmerFor  = 3 * time.Second        // the rewards line shimmers this long after it is shown
)

type kaizenPage int

const (
	pagePulse kaizenPage = iota
	pageFlow
	pageBoard
)

var kaizenPages = []string{"Pulse", "Flow", "Board"}

type kaizenState struct {
	doc       *data.Kaizen
	busy      bool
	busyRange int // the range the load in flight reads
	gen       int // a result carrying an older generation was superseded
	doneAt    time.Time
	doneRange int
	err       string // the last load failed: the first line of why; doc is the result before it
	page      kaizenPage
	top       [3]int    // first visible body line, per page
	born      time.Time // when the numbers on screen arrived: the columns grow from here
	shownAt   time.Time // when the rewards line was shown: it shimmers from here
}

type kaizenMsg struct {
	gen int
	rng int
	doc *data.Kaizen
	err error
}

// kaizenCounters are the tile values that count up: money in cents, times in seconds.
var kaizenCounters = []string{"kz-landed", "kz-cost", "kz-brief", "kz-merge", "kz-yes"}

// loadKaizen starts a load when the view is shown and the range's result is neither in
// flight nor fresher than kaizenFresh. A load of another range replaces the one running.
func (m *model) loadKaizen() tea.Cmd {
	k := &m.kz
	if m.view != viewKaizen || m.runner == nil || m.fatal != nil {
		return nil
	}
	if k.busy && k.busyRange == m.rangeIdx {
		return nil
	}
	if !k.busy && !k.doneAt.IsZero() && k.doneRange == m.rangeIdx && m.now().Sub(k.doneAt) < kaizenFresh {
		return nil
	}
	k.gen++
	k.busy, k.busyRange = true, m.rangeIdx
	r, ctx, since, gen, rng := m.runner, m.ctx, ranges[m.rangeIdx].since, k.gen, m.rangeIdx
	return func() tea.Msg {
		doc, err := r.Kaizen(ctx, since)
		return kaizenMsg{gen: gen, rng: rng, doc: doc, err: err}
	}
}

// applyKaizen takes a load's result whole. A failure keeps the result before it.
func (m *model) applyKaizen(msg kaizenMsg) {
	k := &m.kz
	if msg.gen != k.gen {
		return
	}
	k.busy = false
	if msg.err != nil {
		k.err = firstLine(msg.err.Error())
		return
	}
	k.err = ""
	fresh := k.doc == nil || !reflect.DeepEqual(k.doc, msg.doc)
	k.doc, k.doneAt, k.doneRange = msg.doc, m.now(), msg.rng
	if fresh {
		k.born, k.shownAt = m.now(), m.now()
	}
	m.kaizenRetarget(m.opts.Static)
	m.fixScroll()
}

// kaizenRetarget points the tile counters at the document's numbers; jump skips the count-up.
func (m *model) kaizenRetarget(jump bool) {
	var s data.KaizenSummary
	if m.kz.doc != nil {
		s = m.kz.doc.Summary
	}
	val := func(x *float64, scale float64) float64 {
		if x == nil {
			return 0
		}
		return *x * scale
	}
	for k, v := range map[string]float64{
		"kz-landed": float64(s.Landed), "kz-cost": val(s.Cost.Median, 100),
		"kz-brief": val(s.BriefToPR.Median, 1), "kz-merge": val(s.PRToMerge.Median, 1), "kz-yes": float64(s.FirstPass.Yes),
	} {
		if jump {
			m.counter[k].Jump(v)
		} else {
			m.counter[k].Set(v)
		}
	}
}

// kaizenGrow is how far the chart columns have grown: 0 at the load, 1 after growFor.
func (m *model) kaizenGrow() float64 {
	if m.opts.Static || m.kz.born.IsZero() {
		return 1
	}
	t := min(1, float64(m.now().Sub(m.kz.born))/float64(growFor))
	return 1 - (1-t)*(1-t) // eases out
}

// kaizenShimmer is true while the rewards line on the Pulse page sweeps its gradient.
func (m *model) kaizenShimmer() bool {
	k := &m.kz
	return !m.opts.Static && m.kz.doc != nil && len(m.kz.doc.Rewards) > 0 && m.view == viewKaizen && k.page == pagePulse && !k.shownAt.IsZero() && m.now().Sub(k.shownAt) < shimmerFor
}

// kaizenMoving is true while a load, the growing columns or the shimmer need the fast clock.
func (m *model) kaizenMoving() bool {
	k := &m.kz
	return k.busy || m.kaizenShimmer() || (!k.born.IsZero() && m.now().Sub(k.born) < growFor)
}

// kaizenPage moves one page left or right and shows it from its own scroll position.
func (m *model) kaizenPage(step int) {
	k := &m.kz
	n := len(kaizenPages)
	k.page = kaizenPage((int(k.page) + step + n) % n)
	if k.page == pagePulse {
		k.shownAt = m.now()
	}
}

// kaizenPositions is how many places the current page can be scrolled to.
func (m *model) kaizenPositions() int {
	h := m.h - 3
	return max(1, len(m.kaizenLines(m.th.on(m.th.Bg), m.w, h))-h+1)
}

// landedTasks are the tasks whose pull request merged, in the document's start order.
func landedTasks(doc *data.Kaizen) []data.KaizenTask {
	var out []data.KaizenTask
	for _, t := range doc.Tasks {
		if t.Landed {
			out = append(out, t)
		}
	}
	return out
}

// trendLook compares the latest median with the earlier one; lower is better. Within
// 10% counts as no change.
func (m *model) trendLook(latest, earlier float64) (arrow string, pct int, col color.Color) {
	d := 1.0
	if earlier != 0 {
		d = (latest - earlier) / earlier
	} else if latest == 0 {
		d = 0
	}
	pct = int(math.Round(math.Abs(d) * 100))
	switch {
	case math.Abs(d) <= 0.10+1e-9:
		return "≈", pct, m.th.Muted
	case d < 0:
		return "↓", pct, m.th.Green
	}
	return "↑", pct, m.th.Orange
}
