//go:build !windows

package service

// SetNativeTheme 同步原生标题栏明暗；非 Windows 平台为空操作：
// macOS 用的是隐藏原生标题栏 + 自绘顶栏，Linux 没有对应的标题栏主题接口。
func (s *LauncherService) SetNativeTheme(bool) {}
