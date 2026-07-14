package machine

import (
	"path/filepath"
	"testing"
)

func TestExistingAncestor(t *testing.T) {
	root := t.TempDir()
	path, err := existingAncestor(filepath.Join(root, "models", "not-created"))
	if err != nil {
		t.Fatal(err)
	}
	if path != root {
		t.Fatalf("ancestor = %q, want %q", path, root)
	}
}

func TestDetect(t *testing.T) {
	profile, err := Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if profile.AvailableDiskBytes == 0 || profile.LogicalCPUs == 0 {
		t.Fatalf("incomplete profile: %#v", profile)
	}
}
