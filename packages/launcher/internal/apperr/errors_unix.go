//go:build !windows

package apperr

import (
	"errors"
	"syscall"
)

func isTextFileBusy(err error) bool {
	return errors.Is(err, syscall.ETXTBSY)
}

func isSharingViolation(err error) bool {
	return false
}
