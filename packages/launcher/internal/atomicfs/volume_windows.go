//go:build windows

package atomicfs

import (
	"path/filepath"
	"strings"
)

func sameVolume(left, right string) (bool, error) {
	leftVolume := strings.ToLower(filepath.VolumeName(left))
	rightVolume := strings.ToLower(filepath.VolumeName(right))
	if leftVolume == "" || rightVolume == "" {
		return true, nil
	}
	return leftVolume == rightVolume, nil
}

func isCrossDevice(err error) bool {
	return false
}
