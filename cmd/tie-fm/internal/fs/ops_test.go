package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func waitDone(t *testing.T, op *Op) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for op.Status != StatusCompleted && op.Status != StatusError {
		if time.Now().After(deadline) {
			t.Fatal("operation did not finish in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if op.Status == StatusError {
		t.Fatalf("operation failed: %v", op.Err)
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	ops := NewOperations(nil)
	src, _ := statEntry(srcPath)
	dst, _ := statEntry(destDir)
	op := ops.Copy(src, dst, nil)
	waitDone(t, op)

	got, err := os.ReadFile(filepath.Join(destDir, "src.txt"))
	if err != nil {
		t.Fatalf("copied file missing: %v", err)
	}
	if string(got) != "hello world" {
		t.Fatalf("content mismatch: %q", got)
	}
	// original must still exist after a copy
	if _, err := os.Stat(srcPath); err != nil {
		t.Fatalf("source removed by copy: %v", err)
	}
	if pct := op.PctComplete(); pct != 1 {
		t.Fatalf("PctComplete = %v, want 1", pct)
	}
}

func TestMoveFile(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.txt")
	if err := os.WriteFile(srcPath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(destDir, 0755); err != nil {
		t.Fatal(err)
	}

	ops := NewOperations(nil)
	src, _ := statEntry(srcPath)
	dst, _ := statEntry(destDir)
	op := ops.Move(src, dst, nil)
	waitDone(t, op)

	if _, err := os.Stat(filepath.Join(destDir, "src.txt")); err != nil {
		t.Fatalf("moved file missing: %v", err)
	}
	// move must remove the source
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Fatalf("source still exists after move (err=%v)", err)
	}
}
