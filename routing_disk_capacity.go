package main

import (
	"fmt"
	"math"
)

// Fragment size is the unit of filesystem block counters. The preferred I/O
// block size may be much larger (for example 1 MiB vs 4 KiB on Docker virtiofs).
// Platforms without a fragment-size field pass zero and use their block unit.
func routingFilesystemBytes(blocks, available uint64, blockSize, fragmentSize int64) (uint64, uint64, error) {
	unit := fragmentSize
	if unit == 0 {
		unit = blockSize
	}
	if unit <= 0 || available > blocks {
		return 0, 0, fmt.Errorf("文件系统容量统计单位或计数无效")
	}
	size := uint64(unit)
	if blocks > math.MaxUint64/size {
		return 0, 0, fmt.Errorf("文件系统容量统计超过可表示范围")
	}
	return available * size, blocks * size, nil
}
