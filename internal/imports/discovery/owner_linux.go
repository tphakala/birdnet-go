//go:build linux

package discovery

import (
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// fileOwner returns the owner uid and username of info, or (-1, "") if unknown.
func fileOwner(info os.FileInfo) (uid int, name string) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, ""
	}
	uid = int(st.Uid)
	if u, err := user.LookupId(strconv.Itoa(uid)); err == nil {
		name = u.Username
	}
	return uid, name
}
