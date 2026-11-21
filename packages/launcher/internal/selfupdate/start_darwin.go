//go:build darwin

package selfupdate

import (
	"os/exec"
	"syscall"
)

// startDetached 在新会话中启动 helper：launcher 马上退出，helper 必须脱离父进程继续替换。
func startDetached(helperPath, jobPath string) error {
	command := exec.Command(helperPath, jobPath)
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
