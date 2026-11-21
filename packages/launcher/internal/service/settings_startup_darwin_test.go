//go:build darwin

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadSettingsResetsInvalidInstallDirWithNotice 覆盖启动时安装目录失效的分支：
// 目录被置空回落到默认值，但必须记录可读原因，并在启动后提示一次。
func TestLoadSettingsResetsInvalidInstallDirWithNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	startupNotice = ""

	dir, err := configDir()
	if err != nil {
		t.Fatalf("解析配置目录失败：%v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建配置目录失败：%v", err)
	}
	invalid := "/var/tmp"
	content := `{"baseUrl":"http://127.0.0.1:1/api/v1","installDir":"` + invalid + `"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("写入设置失败：%v", err)
	}

	settings := loadSettings()
	if settings.InstallDir != "" {
		t.Fatalf("非法目录应被置空，实际 %q", settings.InstallDir)
	}
	if !strings.Contains(startupNotice, "「"+invalid+"」不可用") ||
		!strings.Contains(startupNotice, "默认目录") {
		t.Fatalf("启动提示未说明原因：%q", startupNotice)
	}

	svc := &SettingsService{host: &Host{}}
	if notice := svc.GetStartupNotice(); notice == "" {
		t.Fatal("首次读取应返回启动提示")
	}
	if notice := svc.GetStartupNotice(); notice != "" {
		t.Fatalf("启动提示只应出现一次，第二次仍返回：%q", notice)
	}
}
