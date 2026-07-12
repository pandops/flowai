//go:build windows

package executor

// syscall_O_NOFOLLOW_if_safe is a no-op on Windows (no O_NOFOLLOW
// support). The executor falls back to lstat + post-open checks.
func syscall_O_NOFOLLOW_if_safe() int { return 0 }
