//go:build windows

package apperr

import (
	"errors"
	"syscall"
)

func isTextFileBusy(err error) bool {
	return false
}

// Windows 的 ERROR_SHARING_VIOLATION / ERROR_LOCK_VIOLATION。
func isSharingViolation(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == 32 || errno == 33
	}
	return false
}
