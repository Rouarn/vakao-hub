//go:build !darwin && !windows

package service

import "fmt"

// selfUpdate Linux 版暂不支持：当前只有 zip/appimage 安装方式，替换运行中的进程没有确定路径。
func (s *InstallService) selfUpdate(format, filePath string) error {
	return fmt.Errorf("当前平台暂不支持启动器自动更新，请手动下载安装")
}
