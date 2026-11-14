package tui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"isshoni/internal/data"
)

func TestKaizenNoRewardsSettlesWithoutShimmer(t *testing.T) {
	now := t0
	m := New(Options{Now: func() time.Time { return now }})
	m.loading = false
	doc := kaizenDoc(t)
	doc.Rewards = nil
	setKaizen(m, doc, pagePulse)
	m.kz.shownAt, m.kz.born = now, now
	if m.kaizenShimmer() {
		t.Fatal("nothing shimmers without rewards")
	}
	now = now.Add(growFor + time.Millisecond)
	if m.animating() {
		t.Fatal("the ticker settles once the columns have grown")
	}
}

func TestChartGutterHoldsTheShownMaximum(t *testing.T) {
	p := NewTheme(true).on(nil)
	// durations are not monotonic in width: 3599s is "59m59s" but 3600s is "1h00m"
	wide := func(x float64) string {
		if x == 7 {
			return "seven-ish"
		}
		return fmt.Sprintf("%.0f", x)
	}
	vals := []float64{10}
	for i := 0; i < 40; i++ {
		vals = append(vals, 7)
	}
	out := p.chart(chartOpts{vals: vals, median: 6, hasMedian: true, grow: 1, label: wide, pick: func(int) color.Color { return p.th.Blue }}, 20)
	if top := ansi.Strip(out.lines[0]); !strings.HasPrefix(top, "seven-ish ") {
		t.Fatalf("the top label is whole when the shown maximum's label is wider: %q", top)
	}
}

func TestKaizenListsEveryStopSlowedAndTenDecisions(t *testing.T) {
	doc := kaizenDoc(t)
	doc.Stops, doc.Slowed = nil, nil
	for i := 0; i < 15; i++ {
		doc.Stops = append(doc.Stops, data.KaizenStop{TS: fmt.Sprintf("2026-11-%02dT10:00:00+00:00", i+1), Repo: "fixture-alpha", Slug: fmt.Sprintf("fx-stop-%02d", i), Kind: "QUESTION"})
	}
	for i := 0; i < 12; i++ {
		doc.Slowed = append(doc.Slowed, data.KaizenSlow{TS: fmt.Sprintf("2026-11-%02dT10:00:00+00:00", i+1), Repo: "fixture-alpha", Slug: fmt.Sprintf("fx-slow-%02d", i), Text: strings.Repeat("a long slowed text ", 12)})
	}
	var entries []data.OutboxEntry
	for i := 0; i < 12; i++ {
		entries = append(entries, data.OutboxEntry{ID: fmt.Sprintf("d%02d", i), Slug: fmt.Sprintf("fx-dec-%02d", i), Kind: "decision", Status: "open", Text: "DECISION: x"})
	}
	doc.Outbox.Entries = entries
	m := fakeModel(true, 100, 400)
	setKaizen(m, doc, pageFlow)
	out := plain(m)
	for i := 0; i < 15; i++ {
		if !strings.Contains(out, fmt.Sprintf("fx-stop-%02d", i)) {
			t.Fatalf("stop %d is missing", i)
		}
	}
	for i := 0; i < 12; i++ {
		if !strings.Contains(out, fmt.Sprintf("fx-slow-%02d", i)) {
			t.Fatalf("slowed %d is missing", i)
		}
	}
	if strings.Contains(out, "…") || strings.Contains(out, "more lines") {
		t.Fatalf("slowed texts wrap in full:\n%s", out)
	}
	setKaizen(m, doc, pageBoard)
	if n := strings.Count(plain(m), "fx-dec-"); n != 10 {
		t.Fatalf("%d decisions, want 10", n)
	}
}

func TestHeaderLiveCountAtEightyColumnsWithTwelveLive(t *testing.T) {
	for _, view := range []viewID{viewDashboard, viewSession, viewAgents, viewFactory, viewKaizen} {
		m := fakeModel(true, 80, 24)
		for i := 0; i < 12; i++ {
			s := fakeSession(fmt.Sprintf("dddddddd-%04d", i), "alpha", i, 1, 1)
			m.sessions[s.ID], m.seen[s.ID] = s, m.now()
		}
		m.view = view
		if head := strings.Split(plain(m), "\n")[0]; !strings.Contains(head, "12 live") || !strings.Contains(head, "[7d]") || !strings.Contains(head, "5 Kaizen") {
			t.Errorf("view %v: %q", view, head)
		}
	}
}
