//go:build windows

package atomicfs

import (
	"path/filepath"
	"strings"
)

// SameVolume 判断两个路径是否位于同一卷；跨卷时 os.Rename 会失败。
func SameVolume(left, right string) (bool, error) {
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
