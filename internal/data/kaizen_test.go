package data

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const kaizenFixture = "../tui/testdata/kaizen-fixture.json"

// stub is a python that records its arguments and prints the fixture.
func kaizenStubRunner(t *testing.T) (*Runner, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "py.sh")
	body := "#!/bin/sh\necho \"$@\" > " + filepath.Join(dir, "args") + "\nexec cat " + kaizenFixture + "\n"
	abs, _ := filepath.Abs(kaizenFixture)
	body = strings.ReplaceAll(body, kaizenFixture, abs)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Runner{Root: "../..", Python: script}, filepath.Join(dir, "args")
}

func TestKaizenDecodesTheFixtureAndPassesSince(t *testing.T) {
	r, args := kaizenStubRunner(t)
	doc, err := r.Kaizen(context.Background(), "7d")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "-m isshoni kaizen --since 7d --json" {
		t.Fatalf("args: %q", b)
	}
	s := doc.Summary
	if !doc.StoreFound || len(doc.Tasks) != 24 || s.Landed != 20 || s.InFlight != 3 || s.Trend == nil || s.Trend.LatestN != 10 || s.FirstPass.Of != 19 {
		t.Fatalf("summary: %+v", s)
	}
	if s.Cost.Median == nil || *s.Cost.Median != 6.2 || s.ReworkShare == nil {
		t.Fatalf("medians: %+v", s.Cost)
	}
	last := doc.Tasks[20]
	if last.Slug != "fx-quiet-refactor" || !last.Landed || last.PR == nil || last.PR.Number != 120 || last.Cost == nil || last.Cost.Total != 2.31 || last.Rounds == nil || *last.Rounds != 1 {
		t.Fatalf("task: %+v", last)
	}
	if doc.Tasks[3].Cost != nil || doc.Tasks[3].Rounds != nil || doc.Tasks[5].PR != nil || doc.Tasks[5].State != "dismissed" {
		t.Fatal("nulls decode to nil")
	}
	if len(doc.Stops) != 6 || doc.Stops[5].WaitedS != nil || doc.Stops[3].Why == "" || len(doc.Rewards) != 2 || len(doc.Outbox.Entries) != 27 || doc.Outbox.Topics[0].Topic != "brief" {
		t.Fatalf("stops, rewards, outbox: %d %d %d", len(doc.Stops), len(doc.Rewards), len(doc.Outbox.Entries))
	}
	if _, err := r.Kaizen(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(args); strings.TrimSpace(string(b)) != "-m isshoni kaizen --json" {
		t.Fatalf("no range, no --since: %q", b)
	}
}
