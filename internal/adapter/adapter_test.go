package adapter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A program name with a path separator would make exec run a file relative
// to the working directory (the repository) instead of searching PATH.
func TestCallRunsOnlyProgramsOnPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	marker := filepath.Join(dir, "ran")
	if err := os.MkdirAll(filepath.Join(dir, "prove-store-x"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntouch " + marker + "\necho '{\"ok\":true}'\n"
	if err := os.WriteFile(filepath.Join(dir, "prove-store-x", "run"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"prove-store-x/run", "./prove-store-x/run", "prove-store-..", "prove-store-x\\run"} {
		var resp map[string]any
		err := Call(context.Background(), name, map[string]any{"op": "list"}, &resp)
		if err == nil || !strings.Contains(err.Error(), "not a valid adapter name") {
			t.Fatalf("Call(%q) = %v, want an invalid-name error", name, err)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Call ran a file from the working directory")
	}
}
