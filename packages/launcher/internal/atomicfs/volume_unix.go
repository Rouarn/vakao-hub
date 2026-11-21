//go:build !windows

package atomicfs

import (
	"errors"
	"syscall"
)

func sameVolume(left, right string) (bool, error) {
	var leftStat, rightStat syscall.Stat_t
	if err := syscall.Stat(left, &leftStat); err != nil {
		return false, err
	}
	if err := syscall.Stat(right, &rightStat); err != nil {
		return false, err
	}
	return leftStat.Dev == rightStat.Dev, nil
}

func isCrossDevice(err error) bool {
	return errors.Is(err, syscall.EXDEV)
}
