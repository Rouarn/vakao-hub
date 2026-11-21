//go:build darwin

package updater

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"vakaohub/launcher/internal/atomicfs"
)

func DefaultHooks() Hooks {
	return Hooks{
		WaitForExit: waitForExitDarwin,
		Replace:     replaceDarwin,
		Relaunch:    relaunchDarwin,
		Validate:    validateDarwin,
	}
}

func waitForExitDarwin(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		switch {
		case err == nil, errors.Is(err, syscall.EPERM):
			// 进程仍存在；EPERM 说明只是没有权限发信号。
		case errors.Is(err, syscall.ESRCH):
			return nil
		default:
			return err
		}
		if time.Now().After(deadline) {
			return ErrWaitTimeout
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func replaceDarwin(staged, target, backup string) (func() error, error) {
	return atomicfs.SwapWithBackup(staged, target, backup)
}

func relaunchDarwin(target string) error {
	output, err := exec.Command("open", "-a", target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func validateDarwin(path string) error {
	infoPath := filepath.Join(path, "Contents", "Info.plist")
	if info, err := os.Stat(infoPath); err != nil || info.IsDir() {
		return fmt.Errorf("缺少 Contents/Info.plist")
	}
	executable := bundleExecutableDarwin(infoPath)
	macosDir := filepath.Join(path, "Contents", "MacOS")
	if executable != "" {
		target := filepath.Join(macosDir, executable)
		info, err := os.Stat(target)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("可执行文件无效: %s", target)
		}
		return nil
	}
	entries, err := os.ReadDir(macosDir)
	if err != nil || len(entries) == 0 {
		return fmt.Errorf("缺少 Contents/MacOS 可执行文件")
	}
	return nil
}

func bundleExecutableDarwin(infoPath string) string {
	output, err := exec.Command(
		"plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", infoPath,
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
