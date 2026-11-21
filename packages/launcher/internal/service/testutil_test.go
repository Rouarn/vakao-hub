package service

import (
	"path/filepath"
	"runtime"
	"testing"

	"vakaohub/launcher/internal/store"
)

// newTestHost 构造一个不依赖 Wails 运行时的 Host，供单元测试使用。
func newTestHost(t *testing.T, st *store.Store) *Host {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if runtime.GOOS == "windows" {
		// Windows 的 os.UserConfigDir/UserHomeDir 读的是 APPDATA 与 USERPROFILE，
		// 不隔离会把测试写入真实用户配置。
		t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
		t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
		t.Setenv("USERPROFILE", home)
	}
	return newHost(nil, st)
}
