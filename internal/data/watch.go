package data

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Change says that the transcript files of one session changed.
type Change struct {
	SessionID string
	Path      string
	At        time.Time
}

// SessionIDFor maps a transcript path under root to its session id:
// <root>/<project>/<sid>.jsonl or <root>/<project>/<sid>/subagents/<agent>.jsonl.
// ok is false for anything else.
func SessionIDFor(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || !strings.HasSuffix(parts[len(parts)-1], ".jsonl") {
		return "", false
	}
	switch {
	case len(parts) == 2:
		return strings.TrimSuffix(parts[1], ".jsonl"), true
	case len(parts) == 4 && parts[2] == "subagents":
		return parts[1], true
	}
	return "", false
}

// watchableDir reports whether a directory at this relative depth is worth watching.
func watchableDir(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	if rel == "." {
		return true
	}
	parts := strings.Split(rel, string(filepath.Separator))
	switch len(parts) {
	case 1, 2:
		return true
	case 3:
		return parts[2] == "subagents"
	}
	return false
}

// Scan stats every session transcript under root, returning path -> mtime.
func Scan(root string) map[string]time.Time {
	out := map[string]time.Time{}
	projects, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	add := func(dir string) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
				if fi, err := e.Info(); err == nil {
					out[filepath.Join(dir, e.Name())] = fi.ModTime()
				}
			}
		}
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		pd := filepath.Join(root, p.Name())
		add(pd)
		ents, err := os.ReadDir(pd)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				add(filepath.Join(pd, e.Name(), "subagents"))
			}
		}
	}
	return out
}

// Watch reports transcript changes under root until ctx is done. It uses fsnotify
// (adding watches as directories appear) plus a low-frequency mtime poll as a safety
// net. The channel is buffered; events are dropped, never blocked on, when it is full
// (the consumer debounces per session, so a dropped duplicate is harmless).
func Watch(ctx context.Context, root string, poll time.Duration) (<-chan Change, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	out := make(chan Change, 256)
	emit := func(path string, at time.Time) {
		id, ok := SessionIDFor(root, path)
		if !ok {
			return
		}
		select {
		case out <- Change{SessionID: id, Path: path, At: at}:
		default:
		}
	}
	addTree := func(dir string) {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if !watchableDir(root, p) {
					return fs.SkipDir
				}
				_ = w.Add(p)
			}
			return nil
		})
	}
	addTree(root)
	known := Scan(root)
	go func() {
		defer w.Close()
		defer close(out)
		if poll <= 0 {
			poll = 15 * time.Second
		}
		tick := time.NewTicker(poll)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-w.Events:
				if !ok {
					return
				}
				if ev.Has(fsnotify.Create) {
					if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
						addTree(ev.Name)
						// files may already exist inside the new directory
						for p, mt := range Scan(root) {
							if _, seen := known[p]; !seen && strings.HasPrefix(p, ev.Name) {
								known[p] = mt
								emit(p, time.Now())
							}
						}
						continue
					}
				}
				if ev.Has(fsnotify.Write) || ev.Has(fsnotify.Create) {
					if strings.HasSuffix(ev.Name, ".jsonl") {
						if fi, err := os.Stat(ev.Name); err == nil {
							known[ev.Name] = fi.ModTime()
						}
						emit(ev.Name, time.Now())
					}
				}
			case <-w.Errors:
			case <-tick.C:
				cur := Scan(root)
				for p, mt := range cur {
					if old, seen := known[p]; !seen || mt.After(old) {
						emit(p, mt)
					}
				}
				known = cur
			}
		}
	}()
	return out, nil
}
