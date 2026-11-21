package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"vakaohub/launcher/internal/store"
)

// TestSaveSettingsCustomInstallDir 覆盖「安装目录选择」的服务端路径：
// 主目录内的自定义目录应被接受并落盘。
func TestSaveSettingsCustomInstallDir(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("解析 HOME 失败：%v", err)
	}
	dir, err := os.MkdirTemp(home, ".vakaohub-test-")
	if err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	service := NewSettings(host)
	settings, err := service.SaveSettings("http://127.0.0.1:18081/api/v1", dir)
	if err != nil {
		t.Fatalf("保存自定义目录失败：%v", err)
	}
	if settings.InstallDir != dir {
		t.Fatalf("返回的安装目录 = %q，期望 %q", settings.InstallDir, dir)
	}
	if persisted := loadSettings(); persisted.InstallDir != dir {
		t.Fatalf("落盘安装目录 = %q，期望 %q", persisted.InstallDir, dir)
	}
}

// TestSaveSettingsRejectsReadOnlyDir 只读目录必须返回可读的中文错误，且不写入设置。
// Windows 不使用 POSIX 权限位，chmod 造不出只读目录，这条只在类 Unix 上验证。
func TestSaveSettingsRejectsReadOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不支持用 chmod 构造只读目录")
	}
	host := newTestHost(t, store.NewMemory())
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("解析 HOME 失败：%v", err)
	}
	readOnly := filepath.Join(home, "ReadOnly")
	if err := os.MkdirAll(readOnly, 0o500); err != nil {
		t.Fatalf("创建只读目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) })

	service := NewSettings(host)
	before := host.settings.Get()
	_, err = service.SaveSettings("http://127.0.0.1:18081/api/v1", readOnly)
	if err == nil {
		t.Fatal("只读目录应保存失败")
	}
	if !strings.Contains(err.Error(), "没有写入权限") {
		t.Fatalf("错误缺少中文提示：%v", err)
	}
	if after := host.settings.Get(); after.InstallDir != before.InstallDir {
		t.Fatalf("保存失败后设置被修改：%q -> %q", before.InstallDir, after.InstallDir)
	}
}

// TestSaveSettingsRejectsOutsideHome 主目录与 /Applications 之外的路径必须被拒绝。
// 这是 macOS 的目录白名单语义；Windows 允许任意固定盘，按自己的规则校验。
func TestSaveSettingsRejectsOutsideHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 允许任意本机固定盘，不使用 macOS 的主目录白名单")
	}
	host := newTestHost(t, store.NewMemory())
	service := NewSettings(host)

	_, err := service.SaveSettings("http://127.0.0.1:18081/api/v1", "/var/tmp")
	if err == nil {
		t.Fatal("主目录外的路径应被拒绝")
	}
	if !strings.Contains(err.Error(), "只能选择用户主目录内或 /Applications") {
		t.Fatalf("错误缺少中文提示：%v", err)
	}
}
