// pools.go - memory pools for reducing allocations
package diskmanager

import (
	"sync"
)

var (
	// fileInfoPool pools FileInfo slices to reduce allocations during directory walks
	fileInfoPool = sync.Pool{
		New: func() interface{} {
			// Pre-allocate with reasonable capacity for typical directory sizes
			slice := make([]FileInfo, 0, 1000)
			return &slice
		},
	}

	// stringPool pools string slices for error message accumulation
	stringPool = sync.Pool{
		New: func() interface{} {
			slice := make([]string, 0, 100)
			return &slice
		},
	}
)

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

// getStringSlice retrieves a string slice from the pool
func getStringSlice() *[]string {
	slice := stringPool.Get().(*[]string)
	*slice = (*slice)[:0] // Reset length but keep capacity
	return slice
}

// putStringSlice returns a string slice to the pool
func putStringSlice(slice *[]string) {
	if slice == nil || cap(*slice) > 1000 {
		// Don't pool huge slices
		return
	}
	*slice = (*slice)[:0] // Clear the slice
	stringPool.Put(slice)
}
