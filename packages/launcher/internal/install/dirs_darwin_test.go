//go:build darwin

package install

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateInstallDir 覆盖用户可配置的安装目录边界：主目录内可用，
// 系统位置、外置卷、不存在的目录与主目录本身都要拒绝。
func TestValidateInstallDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	custom := filepath.Join(home, "Apps", "Custom")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("创建测试目录失败：%v", err)
	}

	cases := []struct {
		name string
		dir  string
		ok   bool
	}{
		{name: "空值走默认", dir: "", ok: true},
		{name: "主目录内自定义目录", dir: custom, ok: true},
		{name: "波浪号展开", dir: "~/Apps/Custom", ok: true},
		{name: "系统目录", dir: "/usr/local", ok: false},
		{name: "外置卷", dir: "/Volumes/External/Apps", ok: false},
		{name: "不存在", dir: filepath.Join(home, "Missing"), ok: false},
		{name: "主目录本身", dir: home, ok: false},
		{name: "相对路径", dir: "Apps", ok: false},
	}
	for _, item := range cases {
		err := ValidateInstallDir(item.dir)
		if item.ok && err != nil {
			t.Errorf("%s：期望通过，实际 %v", item.name, err)
		}
		if !item.ok && err == nil {
			t.Errorf("%s：期望拒绝，实际通过", item.name)
		}
	}
}

// TestPathRank 确认多目录并存时的优先级：配置目录 > ~/Applications > /Applications > 其它。
func TestPathRank(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	custom := filepath.Join(home, "Apps")
	SetInstallDir(custom)
	t.Cleanup(func() { SetInstallDir("") })

	if got := pathRank(filepath.Join(custom, "Demo.app")); got != 0 {
		t.Errorf("配置目录优先级 = %d，期望 0", got)
	}
	if got := pathRank(filepath.Join(home, "Applications", "Demo.app")); got != 1 {
		t.Errorf("~/Applications 优先级 = %d，期望 1", got)
	}
	if got := pathRank("/Applications/Demo.app"); got != 2 {
		t.Errorf("/Applications 优先级 = %d，期望 2", got)
	}
	if got := pathRank("/opt/Demo.app"); got != 3 {
		t.Errorf("其它位置优先级 = %d，期望 3", got)
	}
}
