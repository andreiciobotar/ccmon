package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// Human abbreviates a count the way the Python CLI does: 999, 1.2k, 3.4M, 5.6G.
func Human(n float64) string {
	for _, u := range []struct {
		lim float64
		suf string
	}{{1e9, "G"}, {1e6, "M"}, {1e3, "k"}} {
		if math.Abs(n) >= u.lim {
			return fmt.Sprintf("%.1f%s", n/u.lim, u.suf)
		}
	}
	return fmt.Sprintf("%d", int64(math.Round(n)))
}

// HumanInt is Human for int64.
func HumanInt(n int64) string { return Human(float64(n)) }

// FmtDuration renders 42s, 3m05s, 1h02m.
func FmtDuration(d time.Duration) string {
	s := int(math.Round(d.Seconds()))
	if s < 0 {
		s = 0
	}
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m, s := s/60, s%60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}

// FmtDurationMS formats a millisecond count, "-" when nil.
func FmtDurationMS(ms *float64) string {
	if ms == nil {
		return "-"
	}
	return FmtDuration(time.Duration(*ms * float64(time.Millisecond)))
}

// width is the display width the libraries use.
func width(s string) int { return ansi.StringWidth(s) }

// trunc cuts s to w display cells, ending in an ellipsis when something was removed.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// cell fits plain s into exactly w cells (truncate, then pad), left- or right-aligned.
func cell(s string, w int, right bool) string {
	s = trunc(s, w)
	pad := w - width(s)
	if pad <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", pad) + s
	}
	return s + strings.Repeat(" ", pad)
}

// fit truncates a styled line to w cells and pads it with painted background.
func fit(line string, w int, p paint) string {
	line = trunc(line, w)
	return line + p.sp(w-width(line))
}

// Counter eases a displayed number toward a target ("count up/down").
type Counter struct {
	Cur, Target float64
}

// counterTau is the easing time constant.
const counterTau = 140 * time.Millisecond

// Set aims the counter at v; it will ease there over a few ticks.
func (c *Counter) Set(v float64) { c.Target = v }

// Jump sets both the displayed and target value.
func (c *Counter) Jump(v float64) { c.Cur, c.Target = v, v }

// Settled reports whether the counter shows its target.
func (c *Counter) Settled() bool { return c.Cur == c.Target }

// Step advances by dt and reports whether it is still moving.
func (c *Counter) Step(dt time.Duration) bool {
	if c.Cur == c.Target {
		return false
	}
	k := 1 - math.Exp(-float64(dt)/float64(counterTau))
	c.Cur += (c.Target - c.Cur) * k
	if math.Abs(c.Target-c.Cur) <= math.Max(0.5, math.Abs(c.Target)*0.0005) {
		c.Cur = c.Target
		return false
	}
	return true
}

// Value is the number to display.
func (c *Counter) Value() float64 { return math.Round(c.Cur) }

// shortModel trims the vendor prefix: claude-haiku-5-5 -> haiku-5-5.
func shortModel(m string) string { return strings.TrimPrefix(m, "claude-") }

// pct renders a percentage with no decimals.
func pct(v float64) string { return fmt.Sprintf("%.0f%%", v) }
