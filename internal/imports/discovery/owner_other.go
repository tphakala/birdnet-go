//go:build !linux

package discovery

import "os"

// fileOwner returns unknown ownership on non-linux platforms.
func fileOwner(_ os.FileInfo) (uid int, name string) { return -1, "" }
