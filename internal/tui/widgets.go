package tui

import (
	"image/color"
	"math"
	"strings"

	"charm.land/bubbles/v2/spinner"
)

var (
	partials   = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}
	plainFills = []string{"█", "▓", "▒", "░", "#", "%", "+", "="}
	spinFrames = spinner.MiniDot.Frames
)

// trackBg is the empty part of a bar: a step lighter than whatever it sits on.
func (p paint) trackBg() color.Color {
	if key24(p.bg) == key24(p.th.Raised) {
		return p.th.Border
	}
	return p.th.Raised
}

// bar draws frac (0..1) of width w in fg; the rest is the track. Sub-cell precision
// via eighth blocks. gradient fills with the signature blue->purple->pink.
func (p paint) bar(w int, frac float64, fg color.Color, gradient bool) string {
	if w <= 0 {
		return ""
	}
	frac = math.Max(0, math.Min(1, frac))
	if p.th.NoColor {
		n := int(math.Round(frac * float64(w)))
		return strings.Repeat("█", n) + strings.Repeat("░", w-n)
	}
	track := p.on(p.trackBg())
	cells := frac * float64(w)
	full := int(cells)
	part := int((cells - float64(full)) * 8)
	colorAt := func(i int) color.Color {
		if !gradient {
			return fg
		}
		t := 0.0
		if w > 1 {
			t = float64(i) / float64(w-1)
		}
		return p.th.gradientAt(t)
	}
	var sb strings.Builder
	for i := 0; i < full && i < w; {
		j := i + 1 // one render per run of one color
		for j < full && j < w && key24(colorAt(j)) == key24(colorAt(i)) {
			j++
		}
		sb.WriteString(track.t(strings.Repeat("█", j-i), colorAt(i)))
		i = j
	}
	used := full
	if part > 0 && full < w {
		sb.WriteString(track.t(string(partials[part]), colorAt(full)))
		used++
	}
	sb.WriteString(track.sp(w - used))
	return sb.String()
}

// swatch is a legend entry's mark for segment i of a stack: the segment's own glyph
// when there are no colors to tell them apart.
func (p paint) swatch(i int, fg color.Color) string {
	if p.th.NoColor {
		return plainFills[i%len(plainFills)]
	}
	return p.t("■", fg)
}

type seg struct {
	val   float64
	color color.Color
}

// stack draws segments proportionally across w cells (largest remainder, so the cell
// counts always sum to w when any value is positive).
func (p paint) stack(w int, segs []seg) string {
	if w <= 0 {
		return ""
	}
	total := 0.0
	for _, s := range segs {
		total += math.Max(0, s.val)
	}
	track := p.on(p.trackBg())
	if total <= 0 {
		if p.th.NoColor {
			return strings.Repeat("░", w)
		}
		return track.sp(w)
	}
	counts := make([]int, len(segs))
	type rem struct {
		i int
		r float64
	}
	var rems []rem
	used := 0
	for i, s := range segs {
		exact := math.Max(0, s.val) / total * float64(w)
		counts[i] = int(exact)
		used += counts[i]
		rems = append(rems, rem{i, exact - float64(counts[i])})
	}
	for used < w {
		best := 0
		for j := range rems {
			if rems[j].r > rems[best].r {
				best = j
			}
		}
		counts[rems[best].i]++
		rems[best].r = -1
		used++
	}
	var sb strings.Builder
	for i, s := range segs {
		if counts[i] == 0 {
			continue
		}
		if p.th.NoColor {
			sb.WriteString(strings.Repeat(plainFills[i%len(plainFills)], counts[i]))
		} else {
			sb.WriteString(p.t(strings.Repeat("█", counts[i]), s.color))
		}
	}
	return sb.String()
}

// pill is a small colored tag; plain rendering uses [brackets].
func (p paint) pill(text string, fg color.Color) string {
	if p.th.NoColor {
		return "[" + text + "]"
	}
	bg := mix(p.bg, fg, 0.2)
	q := p.on(bg)
	return q.b(" "+text+" ", fg)
}

func pillWidth(text string) int { return width(text) + 2 }

// dot is a status dot; phase pulses it between bright and dim.
func (p paint) dot(fg color.Color, bright bool) string {
	if p.th.NoColor {
		if bright {
			return "●"
		}
		return "○"
	}
	if bright {
		return p.b("●", fg)
	}
	return p.t("●", mix(p.bg, fg, 0.45))
}

// spin returns the spinner glyph for a frame counter.
func spin(frame int) string { return spinFrames[frame%len(spinFrames)] }

// block is a rectangle of already-painted lines.
type block []string

// joinCols places blocks side by side with a painted gap; every output line is
// exactly the sum of widths plus gaps, h lines tall.
func joinCols(p paint, h, gap int, widths []int, cols ...block) block {
	out := make(block, h)
	for y := 0; y < h; y++ {
		var sb strings.Builder
		for i, c := range cols {
			if i > 0 {
				sb.WriteString(p.sp(gap))
			}
			if y < len(c) {
				sb.WriteString(fit(c[y], widths[i], p))
			} else {
				sb.WriteString(p.sp(widths[i]))
			}
		}
		out[y] = sb.String()
	}
	return out
}

// boxed wraps lines in a rounded border with an optional title; inner lines render on
// the surface color. Result is exactly w x h.
func (p paint) boxed(title string, lines []string, w, h int, titleFg color.Color) block {
	if w < 4 || h < 2 {
		return make(block, 0)
	}
	in := p.on(p.th.Surface)
	bd := p.th.Border
	out := make(block, 0, h)
	// top
	label := ""
	if title != "" {
		label = " " + trunc(title, w-6) + " "
	}
	rest := w - 3 - width(label)
	if rest < 0 {
		rest = 0
	}
	top := p.t("╭─", bd) + p.b(label, titleFg) + p.t(strings.Repeat("─", rest)+"╮", bd)
	out = append(out, fit(top, w, p))
	for i := 0; i < h-2; i++ {
		ln := ""
		if i < len(lines) {
			ln = lines[i]
		}
		out = append(out, p.t("│", bd)+fit(in.sp(1)+ln, w-2, in)+p.t("│", bd))
	}
	out = append(out, p.t("╰"+strings.Repeat("─", w-2)+"╯", bd))
	return out
}

// center places a block in the middle of a w x h canvas.
func (p paint) center(b block, w, h int) block {
	out := make(block, h)
	bw := 0
	for _, l := range b {
		bw = max(bw, width(l))
	}
	top := max(0, (h-len(b))/2)
	left := max(0, (w-bw)/2)
	for y := 0; y < h; y++ {
		i := y - top
		if i >= 0 && i < len(b) {
			out[y] = fit(p.sp(left)+b[i], w, p)
		} else {
			out[y] = p.sp(w)
		}
	}
	return out
}
