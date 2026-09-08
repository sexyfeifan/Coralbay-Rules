package main

import (
	"math"
	"os"
	"testing"
)

func TestRoutingFilesystemBytesUsesFragmentUnit(t *testing.T) {
	// Captured from the preview container's Docker Desktop virtiofs /data.
	// df -B1 reports 1,995,218,165,760 total bytes; Bsize would inflate it 256x.
	free, total, err := routingFilesystemBytes(487113810, 183337284, 1048576, 4096)
	if err != nil || total != 1995218165760 || free != 750949515264 {
		t.Fatalf("virtiofs units: free=%d total=%d err=%v", free, total, err)
	}
	free, total, err = routingFilesystemBytes(1000, 200, 4096, 0)
	if err != nil || total != 4096000 || free != 819200 {
		t.Fatalf("platform without fragment size: free=%d total=%d err=%v", free, total, err)
	}
	for _, tc := range []struct {
		blocks, available       uint64
		blockSize, fragmentSize int64
	}{
		{1, 1, 0, 0}, {1, 1, 4096, -1}, {100, 101, 4096, 4096}, {math.MaxUint64, 1, 4096, 4096},
	} {
		if _, _, err := routingFilesystemBytes(tc.blocks, tc.available, tc.blockSize, tc.fragmentSize); err == nil {
			t.Fatalf("invalid or overflowing filesystem counters accepted: %+v", tc)
		}
	}
}

func TestRoutingFilesystemSpaceActualMount(t *testing.T) {
	path := os.Getenv("CORALBAY_STATFS_TEST_PATH")
	if path == "" {
		path = t.TempDir()
	}
	free, total, err := routingFilesystemSpace(path)
	if err != nil || total == 0 || free > total {
		t.Fatalf("filesystem capacity: free=%d total=%d err=%v", free, total, err)
	}
	t.Logf("path=%s free_bytes=%d capacity_bytes=%d", path, free, total)
}
