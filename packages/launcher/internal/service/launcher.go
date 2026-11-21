package service

import (
	"os"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/logging"
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

// OpenLog 在系统文件管理器中选中日志文件，供用户查看错误详情。
func (s *LauncherService) OpenLog() error {
	path := logging.Path()
	if path == "" {
		return apperr.New(apperr.KindInternal, "logs", "日志文件尚未生成", nil)
	}
	if _, err := os.Stat(path); err != nil {
		return apperr.New(apperr.KindInternal, "logs", "日志文件尚未生成", err)
	}
	if err := install.Reveal(path); err != nil {
		return apperr.New(apperr.KindInternal, "logs", "无法打开日志文件", err)
	}
	return nil
}

// Quit 由前端在自更新后调用：退出进程让后台脚本完成 bundle 替换并重新打开新版本。
func (s *LauncherService) Quit() {
	if s.host.app == nil {
		return
	}
	s.host.app.Quit()
}
