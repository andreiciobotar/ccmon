package tui

import (
	"fmt"
	"sort"
	"time"
)

const (
	// idleFrac: a stretch with nothing running that is longer than this share of the
	// active time (and at least five minutes) collapses into a break marker.
	idleFrac = 0.05
	idleMin  = 5 * time.Minute
	// breakW is the fixed width of a break marker, wide enough for "┆ 12d 23h ┆".
	breakW = 12
	// minBurstW leaves a burst room for one HH:MM label.
	minBurstW = 5
)

type interval struct{ start, end time.Time }

// axisSeg is a stretch of the axis: a burst of activity drawn in proportion to its
// time, or a break standing for idle time.
type axisSeg struct {
	t0, t1 time.Time
	x, w   int
	brk    bool
}

// timeAxis maps time to timeline columns, skipping idle stretches.
type timeAxis struct {
	segs []axisSeg
	tw   int
}

// newTimeAxis lays the intervals out over tw columns. Gaps between them that are long
// compared with the active time become break markers (as many as fit in half the
// width, longest first); the bursts share the remaining columns by duration, each
// at least minBurstW columns when the width allows, else one.
func newTimeAxis(ivs []interval, tw int) timeAxis {
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].start.Before(ivs[j].start) })
	var merged []interval
	var active time.Duration
	for _, iv := range ivs {
		if n := len(merged); n > 0 && !iv.start.After(merged[n-1].end) {
			if iv.end.After(merged[n-1].end) {
				merged[n-1].end = iv.end
			}
			continue
		}
		merged = append(merged, iv)
	}
	for _, iv := range merged {
		active += iv.end.Sub(iv.start)
	}
	gapAt := func(i int) time.Duration { return merged[i+1].start.Sub(merged[i].end) } // after merged[i]
	var cands []int
	for i := 0; i+1 < len(merged); i++ {
		if g := gapAt(i); g >= idleMin && float64(g) > idleFrac*float64(active) {
			cands = append(cands, i)
		}
	}
	sort.Slice(cands, func(a, b int) bool { return gapAt(cands[a]) > gapAt(cands[b]) })
	cands = cands[:min(len(cands), tw/2/breakW)]
	isBreak := map[int]bool{}
	for _, c := range cands {
		isBreak[c] = true
	}

	var segs []axisSeg
	for i, iv := range merged {
		if n := len(segs); n > 0 && !segs[n-1].brk {
			segs[n-1].t1 = iv.end
		} else {
			segs = append(segs, axisSeg{t0: iv.start, t1: iv.end})
		}
		if isBreak[i] {
			segs = append(segs, axisSeg{t0: iv.end, t1: merged[i+1].start, brk: true})
		}
	}
	// a burst's weight is its duration; instants weigh a second
	weight := func(sg axisSeg) float64 { return max(float64(sg.t1.Sub(sg.t0)), float64(time.Second)) }
	rem, sum := tw-len(cands)*breakW, 0.0
	for _, sg := range segs {
		if !sg.brk {
			sum += weight(sg)
		}
	}
	bursts := len(segs) - len(cands)
	rem = max(rem, bursts)
	floor := 1
	if bursts > 0 && rem/bursts >= minBurstW {
		floor = minBurstW
	}
	// the floor is paid first; only what is left is split by duration
	share := float64(rem - floor*bursts)
	x, left, widest := 0, rem, -1
	for i := range segs {
		sg := &segs[i]
		sg.x = x
		if sg.brk {
			sg.w = breakW
		} else {
			sg.w = floor + int(weight(*sg)/sum*share)
			left -= sg.w
			if widest < 0 || sg.w > segs[widest].w {
				widest = i
			}
		}
		x += sg.w
	}
	if widest >= 0 && left != 0 { // rounding: the widest burst absorbs the difference
		segs[widest].w += left
		for i := widest + 1; i < len(segs); i++ {
			segs[i].x += left
		}
	}
	return timeAxis{segs: segs, tw: tw}
}

// span is the first and last moment on the axis.
func (a timeAxis) span() (time.Time, time.Time) {
	return a.segs[0].t0, a.segs[len(a.segs)-1].t1
}

// x is the column of t; a moment inside a break maps to the burst after it.
func (a timeAxis) x(t time.Time) int {
	for _, sg := range a.segs {
		if sg.brk || t.After(sg.t1) {
			continue
		}
		if !t.After(sg.t0) {
			return sg.x
		}
		frac := float64(t.Sub(sg.t0)) / float64(sg.t1.Sub(sg.t0))
		return sg.x + min(sg.w-1, int(frac*float64(sg.w)))
	}
	last := a.segs[len(a.segs)-1]
	return last.x + last.w - 1
}

// marker is the text a break shows, centred in its columns.
func (sg axisSeg) marker() string {
	return "┆ " + fmtGap(sg.t1.Sub(sg.t0)) + " ┆"
}

// markerEdges are the columns of the marker's two ┆, which lane and axis both draw.
func (sg axisSeg) markerEdges() (int, int) {
	l := sg.x + (sg.w-width(sg.marker()))/2
	return l, l + width(sg.marker()) - 1
}

// fmtGap renders an idle length: 45m, 5h 12m, 2d 18h.
func fmtGap(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
