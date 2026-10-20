//go:build linux

package spectrogram

import "golang.org/x/sys/unix"

// lowerThreadPriority sets the calling OS thread to nice nativeRenderNice. Linux
// nice values are per thread, so only the thread the caller is locked to changes.
func lowerThreadPriority() error {
	return unix.Setpriority(unix.PRIO_PROCESS, unix.Gettid(), nativeRenderNice)
}
