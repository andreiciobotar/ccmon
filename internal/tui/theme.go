// Package tui is the ccmon live terminal dashboard.
package tui

import (
	"hash/fnv"
	"image/color"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
)

// Theme is the fixed dark, HEY-inspired truecolor palette. With NoColor set every
// style renders without colors (bold and reverse stay) and glyphs carry the meaning.
type Theme struct {
	NoColor bool

	Bg, Surface, Raised, Border color.Color
	Text, Muted, Faint          color.Color
	Blue, Purple, Pink, Yellow  color.Color
	Teal, Green, Orange, Red    color.Color

	cache map[styleKey]lipgloss.Style
}

type styleKey struct {
	fg, bg uint32
	bold   bool
	rev    bool
}

// NewTheme returns the palette; noColor selects the plain rendering.
func NewTheme(noColor bool) *Theme {
	c := lipgloss.Color
	return &Theme{
		NoColor: noColor,
		Bg:      c("#12111C"), Surface: c("#1B1A2B"), Raised: c("#25243A"), Border: c("#343350"),
		Text: c("#ECEAF7"), Muted: c("#8E8BA8"), Faint: c("#5C5A75"),
		Blue: c("#5E9BFF"), Purple: c("#A78BFA"), Pink: c("#FF6FAE"), Yellow: c("#FFD34E"),
		Teal: c("#3FD8C4"), Green: c("#58D68D"), Orange: c("#FF9F5A"), Red: c("#FF5C6C"),
		cache: map[styleKey]lipgloss.Style{},
	}
}

func rgbOf(c color.Color) (r, g, b float64) {
	if c == nil {
		return 0, 0, 0
	}
	R, G, B, _ := c.RGBA()
	return float64(R >> 8), float64(G >> 8), float64(B >> 8)
}

func key24(c color.Color) uint32 {
	if c == nil {
		return 0
	}
	r, g, b := rgbOf(c)
	return 1<<24 | uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

// mix blends a toward b by t (0..1).
func mix(a, b color.Color, t float64) color.Color {
	t = math.Max(0, math.Min(1, t))
	ar, ag, ab := rgbOf(a)
	br, bg, bb := rgbOf(b)
	return color.RGBA{
		R: uint8(math.Round(ar + (br-ar)*t)),
		G: uint8(math.Round(ag + (bg-ag)*t)),
		B: uint8(math.Round(ab + (bb-ab)*t)),
		A: 255,
	}
}

// gradientAt samples blue -> purple -> pink at t in 0..1.
func (th *Theme) gradientAt(t float64) color.Color {
	t = math.Max(0, math.Min(1, t))
	if t < 0.5 {
		return mix(th.Blue, th.Purple, t*2)
	}
	return mix(th.Purple, th.Pink, (t-0.5)*2)
}

// AgentColor is the one stable color of an agent type.
func (th *Theme) AgentColor(agent string) color.Color {
	switch agent {
	case "main":
		return th.Yellow
	case "explorer":
		return th.Teal
	case "coder":
		return th.Blue
	case "reviewer":
		return th.Pink
	case "general-purpose":
		return th.Purple
	case "Explore":
		return th.Green
	case "Plan":
		return th.Orange
	case "fork":
		return mix(th.Purple, th.Text, 0.55) // faint lavender
	}
	return th.hashColor(agent)
}

// ModelColor is a stable color per model; families line up with the agents that
// usually run on them (fable/main, haiku/explorer, sonnet/coder, opus/reviewer).
func (th *Theme) ModelColor(model string) color.Color {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "fable"):
		return th.Yellow
	case strings.Contains(m, "haiku"):
		return th.Teal
	case strings.Contains(m, "sonnet"):
		return th.Blue
	case strings.Contains(m, "opus"):
		return th.Pink
	}
	return th.hashColor(model)
}

func (th *Theme) hashColor(name string) color.Color {
	pal := []color.Color{th.Blue, th.Purple, th.Pink, th.Yellow, th.Teal, th.Green, th.Orange}
	h := fnv.New32a()
	h.Write([]byte(name))
	return pal[int(h.Sum32())%len(pal)]
}

// paint renders text on a fixed background so no seams show between segments.
type paint struct {
	th *Theme
	bg color.Color
}

func (th *Theme) on(bg color.Color) paint { return paint{th: th, bg: bg} }

func (p paint) on(bg color.Color) paint { return paint{th: p.th, bg: bg} }

func (p paint) style(fg color.Color, bold bool) lipgloss.Style {
	return p.styleRev(fg, bold, false)
}

func (p paint) styleRev(fg color.Color, bold, rev bool) lipgloss.Style {
	th := p.th
	k := styleKey{bold: bold, rev: rev}
	if !th.NoColor {
		k.fg, k.bg = key24(fg), key24(p.bg)
	}
	if s, ok := th.cache[k]; ok {
		return s
	}
	s := lipgloss.NewStyle()
	if th.NoColor {
		th.cache[k] = s
		return s
	}
	if fg != nil {
		s = s.Foreground(fg)
	}
	if p.bg != nil {
		s = s.Background(p.bg)
	}
	if bold {
		s = s.Bold(true)
	}
	if rev {
		s = s.Reverse(true)
	}
	th.cache[k] = s
	return s
}

// t paints s with fg on the background.
func (p paint) t(s string, fg color.Color) string {
	if s == "" {
		return ""
	}
	return p.style(fg, false).Render(s)
}

// b paints s bold.
func (p paint) b(s string, fg color.Color) string {
	if s == "" {
		return ""
	}
	return p.style(fg, true).Render(s)
}

// sp is n painted spaces.
func (p paint) sp(n int) string {
	if n <= 0 {
		return ""
	}
	return p.style(p.th.Text, false).Render(strings.Repeat(" ", n))
}

// gradient paints s with the blue -> purple -> pink gradient, one color per rune.
func (p paint) gradient(s string, bold bool) string {
	rs := []rune(s)
	var sb strings.Builder
	for i, r := range rs {
		t := 0.0
		if len(rs) > 1 {
			t = float64(i) / float64(len(rs)-1)
		}
		sb.WriteString(p.style(p.th.gradientAt(t), bold).Render(string(r)))
	}
	return sb.String()
}
