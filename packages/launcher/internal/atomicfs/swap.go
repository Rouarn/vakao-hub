// Package atomicfs 提供同卷暂存目录与原子换入，确保安装失败时旧版本仍然完整。
package atomicfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
)

const (
	stagePrefix = ".vakaohub-stage-"
	oldMarker   = ".vakaohub-old-"
)

// StageDirFor 在 target 的同级目录创建暂存目录，保证与 target 同文件系统，
// 使 os.Rename 具备原子性；不要放在 /tmp 或系统临时目录。
func StageDirFor(target string) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", apperr.New(apperr.KindInternal, "atomicfs.stage", "安装路径为空，无法准备暂存目录", nil)
	}
	absolute, err := filepath.Abs(target)
	if err != nil {
		return "", apperr.New(apperr.KindInternal, "atomicfs.stage", "安装路径无效", err)
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", apperr.FromOS(err, "atomicfs.stage")
	}
	stage, err := os.MkdirTemp(parent, stagePrefix)
	if err != nil {
		return "", apperr.FromOS(err, "atomicfs.stage")
	}
	return stage, nil
}

// Swap 把 staged 换入 target：
// 1. target 存在则先 rename 到同级备份；
// 2. staged rename 到 target；
// 3. 失败则把备份 rename 回 target；
// 4. 成功返回 rollback，用于后续校验/拉起失败时回滚。
//
// 成功路径的备份由调用方在最终确认后通过 CleanupBackups 清理。
func Swap(staged, target string) (func() error, error) {
	if strings.TrimSpace(staged) == "" || strings.TrimSpace(target) == "" {
		return nil, apperr.New(apperr.KindInternal, "atomicfs.swap", "暂存路径或安装路径为空", nil)
	}
	stagedPath, err := filepath.Abs(staged)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "atomicfs.swap", "暂存路径无效", err)
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "atomicfs.swap", "安装路径无效", err)
	}

	same, err := sameVolume(filepath.Dir(stagedPath), filepath.Dir(targetPath))
	if err != nil {
		return nil, apperr.FromOS(err, "atomicfs.swap")
	}
	if !same {
		return nil, apperr.New(
			apperr.KindUnsupported,
			"atomicfs.swap",
			"暂存目录与安装目录不在同一磁盘，无法原子替换",
			nil,
		)
	}

	targetExists := false
	if _, statErr := os.Stat(targetPath); statErr == nil {
		targetExists = true
	} else if !os.IsNotExist(statErr) {
		return nil, apperr.FromOS(statErr, "atomicfs.swap")
	}

	parent := filepath.Dir(targetPath)
	backup := filepath.Join(parent, "."+filepath.Base(targetPath)+oldMarker+randomID())
	if targetExists {
		if err := os.Rename(targetPath, backup); err != nil {
			return nil, swapError(err)
		}
	}

	if err := os.Rename(stagedPath, targetPath); err != nil {
		var restoreErr error
		if targetExists {
			restoreErr = os.Rename(backup, targetPath)
		}
		if restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("恢复旧版本失败: %w", restoreErr))
		}
		return nil, swapError(err)
	}

	rollback := func() error {
		if err := os.RemoveAll(targetPath); err != nil {
			return err
		}
		if !targetExists {
			return nil
		}
		return os.Rename(backup, targetPath)
	}
	return rollback, nil
}

// CleanupBackups 删除 target 同级残留的旧版本备份；失败只返回给调用方记日志。
func CleanupBackups(target string) error {
	pattern := filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+oldMarker+"*")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}
	var result error
	for _, match := range matches {
		if removeErr := os.RemoveAll(match); removeErr != nil {
			result = errors.Join(result, removeErr)
		}
	}
	return result
}

// CleanupStale 清理目录下超过 maxAge 的暂存与备份残留。
func CleanupStale(dir string, maxAge time.Duration) {
	if strings.TrimSpace(dir) == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	deadline := time.Now().Add(-maxAge)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, stagePrefix) && !strings.Contains(name, oldMarker) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(deadline) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
}

func swapError(err error) error {
	if isCrossDevice(err) {
		return apperr.New(
			apperr.KindUnsupported,
			"atomicfs.swap",
			"暂存目录与安装目录不在同一磁盘，无法原子替换",
			err,
		)
	}
	return apperr.FromOS(err, "atomicfs.swap")
}

func randomID() string {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
