// pools.go - memory pools for reducing allocations
package diskmanager

import (
	"sync"
)

// fileInfoPool pools FileInfo slices to reduce allocations during directory walks
var fileInfoPool = sync.Pool{
	New: func() any {
		// Pre-allocate with reasonable capacity for typical directory sizes
		slice := make([]FileInfo, 0, 1000)
		return &slice
	},
}

// getFileInfoSlice retrieves a FileInfo slice from the pool
func getFileInfoSlice() *[]FileInfo {
	slice := fileInfoPool.Get().(*[]FileInfo)
	*slice = (*slice)[:0] // Reset length but keep capacity
	return slice
}

// putFileInfoSlice returns a FileInfo slice to the pool
func putFileInfoSlice(slice *[]FileInfo) {
	if slice == nil || cap(*slice) > 10000 {
		// Don't pool huge slices to avoid memory bloat
		return
	}
	*slice = (*slice)[:0] // Clear the slice
	fileInfoPool.Put(slice)
}
