//go:build windows

package install

import (
	"fmt"
	"os/exec"
)

// Reveal 在资源管理器中选中指定文件或目录。
func Reveal(path string) error {
	cmd := exec.Command("explorer", "/select,"+path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法打开资源管理器定位 %s: %w", path, err)
	}
	_ = cmd.Process.Release()
	return nil
}
