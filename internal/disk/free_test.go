package disk

import "testing"

func TestFreeOnExistingDir(t *testing.T) {
	free, err := Free(t.TempDir())
	if err != nil {
		t.Fatalf("free: %v", err)
	}
	if free == 0 {
		t.Fatal("expected non-zero free space")
	}
}

func TestFreeWalksUpForMissingPath(t *testing.T) {
	root := t.TempDir()
	free, err := Free(root + "/does/not/exist/yet")
	if err != nil {
		t.Fatalf("free: %v", err)
	}
	if free == 0 {
		t.Fatal("expected non-zero free space")
	}
}

func TestUsageReportsTotalAndFree(t *testing.T) {
	free, total, err := Usage(t.TempDir())
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if total == 0 {
		t.Fatal("expected non-zero total space")
	}
	if free == 0 {
		t.Fatal("expected non-zero free space")
	}
	if free > total {
		t.Fatalf("free %d > total %d", free, total)
	}
}

func TestUsageWalksUpForMissingPath(t *testing.T) {
	root := t.TempDir()
	fromMissing, totalFromMissing, err := Usage(root + "/nested/missing")
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	fromRoot, totalFromRoot, err := Usage(root)
	if err != nil {
		t.Fatalf("usage root: %v", err)
	}
	if totalFromMissing != totalFromRoot {
		t.Fatalf("totals differ: %d vs %d", totalFromMissing, totalFromRoot)
	}
	if fromMissing > totalFromMissing || fromRoot > totalFromRoot {
		t.Fatal("free space exceeds total")
	}
}
