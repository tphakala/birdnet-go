//go:build !unix

package securefs

import "os"

// openRegularFlags are the flags OpenRegularRel opens with. Non-unix platforms
// have no FIFOs in ordinary directories, and the unix O_NONBLOCK value has no
// documented meaning for os.Root on Windows, so the open is a plain read.
const openRegularFlags = os.O_RDONLY
