//go:build !windows

package install

import "vakaohub/launcher/internal/apperr"

// UninstallInstaller 只在 Windows 上存在；其它平台走 Remove 删除托管目录。
func UninstallInstaller(string) error {
	return apperr.New(apperr.KindUnsupported, "uninstall", "当前平台不支持调用系统卸载程序", nil)
}
