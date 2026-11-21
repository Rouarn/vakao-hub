package service

import (
	launcherversion "vakaohub/launcher/internal/version"
)

type LauncherService struct {
	host *Host
}

func NewLauncher(host *Host) *LauncherService {
	return &LauncherService{host: host}
}

// GetAppVersion 返回构建时注入的启动器版本（开发构建为空串）。
func (s *LauncherService) GetAppVersion() string {
	return launcherversion.Version
}

func (s *LauncherService) OpenURL(url string) {
	if s.host.app == nil {
		return
	}
	_ = s.host.app.Browser.OpenURL(url)
}

// Quit 由前端在自更新后调用：退出进程让后台脚本完成 bundle 替换并重新打开新版本。
func (s *LauncherService) Quit() {
	if s.host.app == nil {
		return
	}
	s.host.app.Quit()
}
