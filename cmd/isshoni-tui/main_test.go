package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsoluteResolvesAgainstWorkingDirectory(t *testing.T) {
	wd := t.TempDir()
	if err := os.Mkdir(filepath.Join(wd, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(wd)
	got := absolute("sub")
	if !filepath.IsAbs(got) {
		t.Fatalf("relative dir not made absolute: %q", got)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(wd, "sub"))
	if real, _ := filepath.EvalSymlinks(got); real != want {
		t.Fatalf("absolute(sub) = %q, want %q", real, want)
	}
	if got := absolute(""); got != "" {
		t.Fatalf("empty stays empty, got %q", got)
	}
	if got := absolute("/x/y"); got != "/x/y" {
		t.Fatalf("an absolute dir stays, got %q", got)
	}
}
