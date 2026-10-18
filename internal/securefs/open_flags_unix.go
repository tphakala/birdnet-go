//go:build unix

package securefs

import (
	"os"
	"syscall"
)

// openRegularFlags are the flags OpenRegularRel opens with. O_NONBLOCK keeps
// the open from blocking on a FIFO or device node until a peer appears; the
// handle is then checked for being a regular file. It does not change reads
// on a regular file.
const openRegularFlags = os.O_RDONLY | syscall.O_NONBLOCK
