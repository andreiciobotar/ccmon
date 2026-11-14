// Command ccmon-tui is a live terminal dashboard over the ccmon CLI.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ccmon/internal/data"
	"ccmon/internal/tui"
)

func main() {
	var (
		projects = flag.String("projects-dir", "", "transcripts root (default ~/.claude/projects)")
		agents   = flag.String("agents-dir", "", "agent definitions dir, passed to ccmon")
		prices   = flag.String("prices", "", "price file, passed to ccmon (default ccmon/prices.toml)")
		rng      = flag.String("range", "7d", "initial range: 24h, 7d, 30d, all")
		snapshot = flag.String("snapshot", "", "render one frame to stdout and exit: dashboard, session, agents, help")
		size     = flag.String("size", "140x40", "snapshot size WxH")
		session  = flag.String("session", "", "session id prefix for --snapshot session")
		noColor  = flag.Bool("no-color", false, "plain rendering (also honors NO_COLOR)")
		follow   = flag.Bool("follow", false, "start in follow mode")
	)
	flag.Parse()

	root, rootErr := data.LocateRoot()
	// a missing root is not fatal here: the TUI reports it on screen
	r := &data.Runner{Root: root, ProjectsDir: absolute(*projects), AgentsDir: absolute(*agents), Prices: absolute(*prices), Err: rootErr}
	nc := *noColor || os.Getenv("NO_COLOR") != ""
	o := tui.Options{Runner: r, Range: *rng, NoColor: nc, Follow: *follow}

	if *snapshot != "" {
		w, h, err := parseSize(*size)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccmon-tui:", err)
			os.Exit(2)
		}
		switch *snapshot {
		case "dashboard", "session", "agents", "help":
		default:
			fmt.Fprintln(os.Stderr, "ccmon-tui: unknown snapshot view", *snapshot)
			os.Exit(2)
		}
		out, err := tui.Snapshot(tui.SnapshotOptions{Options: o, View: *snapshot, Session: *session, W: w, H: h})
		if err != nil {
			fmt.Fprintln(os.Stderr, "ccmon-tui:", err)
			os.Exit(1)
		}
		fmt.Println(out)
		return
	}
	if err := tui.Run(o); err != nil {
		fmt.Fprintln(os.Stderr, "ccmon-tui:", err)
		os.Exit(1)
	}
}

// absolute resolves a directory flag against the user's working directory, because
// the python side runs with the repo root as its own.
func absolute(dir string) string {
	if dir == "" {
		return ""
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func parseSize(s string) (int, int, error) {
	a, b, ok := strings.Cut(strings.ToLower(s), "x")
	w, e1 := strconv.Atoi(a)
	h, e2 := strconv.Atoi(b)
	if !ok || e1 != nil || e2 != nil || w < 1 || h < 1 {
		return 0, 0, fmt.Errorf("invalid --size %q (want WxH)", s)
	}
	return w, h, nil
}
