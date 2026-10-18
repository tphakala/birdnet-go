//go:build unix

package securefs

import (
	"os"
	"syscall"
)

// openRegularFlags are the flags OpenRegularRel opens with. O_NONBLOCK keeps
// the open from blocking on a FIFO until a writer appears; the handle is then
// checked for being a regular file. It does not change reads on a regular
// file. Linux does not apply it to block devices.
const openRegularFlags = os.O_RDONLY | syscall.O_NONBLOCK
