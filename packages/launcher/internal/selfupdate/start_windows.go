//go:build windows

package selfupdate

import (
	"os/exec"
	"syscall"
)

const (
	createBreakawayFromJob = 0x01000000
	createNoWindow         = 0x08000000
	detachedProcess        = 0x00000008
)

// startDetached 启动 helper：优先脱离 Job 对象，避免 launcher 退出时被系统一并结束。
// 少数环境不允许 breakaway（例如外层 Job 未开启嵌套），失败后回退到普通分离进程。
func startDetached(helperPath, jobPath string) error {
	err := launchWindows(helperPath, jobPath, detachedProcess|createBreakawayFromJob|createNoWindow)
	if err == nil {
		return nil
	}
	return launchWindows(helperPath, jobPath, detachedProcess|createNoWindow)
}

func launchWindows(helperPath, jobPath string, flags uint32) error {
	command := exec.Command(helperPath, jobPath)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: flags,
	}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
