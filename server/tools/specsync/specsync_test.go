package specsync

import (
	"path/filepath"
	"testing"
)

// TestSpecSync is the spec/code drift gate: it fails when code
// references a spec token that does not exist, or when the spec defines
// a canonical anchor twice. Spec tags not yet referenced from code are
// reported, not failed (spec-only surface is expected).
func TestSpecSync(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	report, err := Check(root)
	t.Log(report)
	if err != nil {
		t.Fatal(err)
	}
}
