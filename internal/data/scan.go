package data

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Scan stats the transcripts under root (<root>/<project>/<sid>.jsonl and
// <root>/<project>/<sid>/subagents/*.jsonl) and returns the newest mtime per session id.
// A session exists while its main transcript does. The result is only trustworthy
// when err is nil.
func Scan(root string) (map[string]time.Time, error) {
	out := map[string]time.Time{}
	projects, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		pd := filepath.Join(root, p.Name())
		ents, err := os.ReadDir(pd)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, err
		}
		for _, e := range ents {
			if id, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && !e.IsDir() {
				if fi, err := e.Info(); err == nil {
					out[id] = fi.ModTime()
				}
			}
		}
		for _, e := range ents {
			if t, ok := out[e.Name()]; ok && e.IsDir() {
				out[e.Name()] = newest(filepath.Join(pd, e.Name(), "subagents"), t)
			}
		}
	}
	return out, nil
}

// newest is the latest mtime among the .jsonl files in dir, or t.
func newest(dir string, t time.Time) time.Time {
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			if fi, err := e.Info(); err == nil && fi.ModTime().After(t) {
				t = fi.ModTime()
			}
		}
	}
	return t
}
