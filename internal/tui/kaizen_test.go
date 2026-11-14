package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"isshoni/internal/data"
)

// ---- fixtures ---------------------------------------------------------------------

// kaizenDoc reads the made-up document the pages are tested on. Unknown keys are an error,
// so the Go types and the fixture cannot drift apart.
func kaizenDoc(t testing.TB) *data.Kaizen {
	t.Helper()
	b, err := os.ReadFile("testdata/kaizen-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var d data.Kaizen
	if err := dec.Decode(&d); err != nil {
		t.Fatal(err)
	}
	return &d
}

// fewDoc is two landed tasks, no trend and no rewards.
func fewDoc(t testing.TB) *data.Kaizen {
	d := kaizenDoc(t)
	d.Tasks = d.Tasks[:2]
	s := &d.Summary
	s.Tasks, s.Landed, s.InFlight = 2, 2, 0
	s.Cost, s.BriefToPR, s.PRToMerge, s.Rounds = data.Median{Median: ptr(9.9), N: 2}, data.Median{Median: ptr(3000.0), N: 2}, data.Median{Median: ptr(750.0), N: 2}, data.Median{Median: ptr(3.5), N: 2}
	s.FirstPass.Yes, s.FirstPass.Of, s.Trend = 0, 2, nil
	d.Rewards = nil
	return d
}

// setKaizen puts the model on a page of the kaizen view holding doc as the last result.
func setKaizen(m *model, doc *data.Kaizen, page kaizenPage) {
	m.view = viewKaizen
	m.kz.doc, m.kz.doneAt, m.kz.doneRange, m.kz.page = doc, m.now(), m.rangeIdx, page
	m.kaizenRetarget(true)
	m.fixScroll()
}

func kaizenModel(t testing.TB, noColor bool, w, h int, page kaizenPage) *model {
	m := fakeModel(noColor, w, h)
	setKaizen(m, kaizenDoc(t), page)
	return m
}

// ---- goldens and screenshots ------------------------------------------------------

func TestGoldenKaizen(t *testing.T) {
	shots := os.Getenv("ISSHONI_SHOT_DIR")
	doc := func(t *testing.T) *data.Kaizen { return kaizenDoc(t) }
	for _, c := range []struct {
		name string
		w, h int
		page kaizenPage
		doc  func(t *testing.T) *data.Kaizen
		gold bool
	}{
		{"kaizen-140x40", 140, 40, pagePulse, doc, true},
		{"kaizen-100x30", 100, 30, pagePulse, doc, true},
		{"kaizen-80x24", 80, 24, pagePulse, doc, true},
		{"kaizen-flow-140x40", 140, 40, pageFlow, doc, true},
		{"kaizen-flow-100x30", 100, 30, pageFlow, doc, false},
		{"kaizen-flow-140x100", 140, 100, pageFlow, doc, true},
		{"kaizen-board-140x100", 140, 100, pageBoard, doc, true},
		{"kaizen-flow-80x24", 80, 24, pageFlow, doc, true},
		{"kaizen-board-140x40", 140, 40, pageBoard, doc, true},
		{"kaizen-board-100x30", 100, 30, pageBoard, doc, false},
		{"kaizen-board-80x24", 80, 24, pageBoard, doc, true},
		{"kaizen-empty-80x24", 80, 24, pagePulse, func(*testing.T) *data.Kaizen { return &data.Kaizen{} }, true},
		{"kaizen-few-100x30", 100, 30, pagePulse, func(t *testing.T) *data.Kaizen { return fewDoc(t) }, true},
		{"kaizen-wide-200x50", 200, 50, pagePulse, doc, false},
		{"kaizen-narrow-60x20", 60, 20, pagePulse, doc, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if shots != "" {
				m := fakeModel(false, c.w, c.h)
				setKaizen(m, c.doc(t), c.page)
				if err := os.WriteFile(filepath.Join(shots, c.name+".ansi"), []byte(m.Render()+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if !c.gold {
				return
			}
			m := fakeModel(true, c.w, c.h)
			setKaizen(m, c.doc(t), c.page)
			got := m.Render() + "\n"
			path := filepath.Join("testdata", c.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Fatalf("%s changed; run go test ./internal/tui -update and review the diff\n--- got ---\n%s", c.name, got)
			}
		})
	}
}
