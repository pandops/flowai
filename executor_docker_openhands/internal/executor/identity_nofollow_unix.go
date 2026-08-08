//go:build !windows

package executor

import "syscall"

// syscall_O_NOFOLLOW_if_safe adds O_NOFOLLOW when the platform supports
// it (POSIX). Windows / WASI handles rejection via lstat post-open.
func syscall_O_NOFOLLOW_if_safe() int {
	return syscall.O_NOFOLLOW
}
