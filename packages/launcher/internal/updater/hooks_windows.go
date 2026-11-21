//go:build windows

package updater

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
)

const createNoWindow = 0x08000000

func DefaultHooks() Hooks {
	return Hooks{
		WaitForExit: waitForExitWindows,
		Replace:     replaceWindows,
		Relaunch:    relaunchWindows,
		Validate:    validateWindows,
	}
}

func waitForExitWindows(pid int, timeout time.Duration) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		// 进程已经不存在，视为已退出。
		return nil
	}
	done := make(chan error, 1)
	go func() {
		_, waitErr := process.Wait()
		done <- waitErr
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return ErrWaitTimeout
	}
}

func replaceWindows(staged, target, backup string) (func() error, error) {
	var last error
	// 目标 exe 仍在退出过程中时 rename 会失败，最多重试 40×250ms。
	for attempt := 0; attempt < 40; attempt++ {
		rollback, err := atomicfs.SwapWithBackup(staged, target, backup)
		if err == nil {
			return rollback, nil
		}
		last = err
		if apperr.KindOf(err) != apperr.KindBusy {
			return nil, err
		}
		time.Sleep(250 * time.Millisecond)
	}
	return nil, last
}

func relaunchWindows(target string) error {
	command := exec.Command(target)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动新版本失败: %w", err)
	}
	return command.Process.Release()
}

func validateWindows(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	header := make([]byte, 2)
	if _, err := file.Read(header); err != nil {
		return fmt.Errorf("读取可执行文件头失败: %w", err)
	}
	if header[0] != 'M' || header[1] != 'Z' {
		return fmt.Errorf("不是有效的 Windows 可执行文件")
	}
	return nil
}
