package service

import (
	"path/filepath"
	"strings"
	"testing"

	"vakaohub/launcher/internal/store"
)

// TestLaunchSelfIsNoop 启动器自身已在前台运行，Launch 不应再执行 open，
// 否则会去打开安装记录里可能已失效的旧路径。
func TestLaunchSelfIsNoop(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	catalog := NewCatalog(host)
	if err := catalog.Launch("vakaohub"); err != nil {
		t.Fatalf("Launch(vakaohub) 应无操作，实际返回：%v", err)
	}
}

// TestLaunchMissingPathReportsFriendlyError 记录路径失效且扫描不到应用时，
// 返回中文提示并清掉失效记录，而不是把 open 的 exit status 抛给用户。
func TestLaunchMissingPathReportsFriendlyError(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	key := "ghost-launch-test"
	name := "Ghost Launch Test 不存在"
	if err := host.store.Put(store.Installed{
		AppKey:      key,
		Name:        name,
		InstallPath: filepath.Join(t.TempDir(), "Applications", "Ghost.app"),
	}); err != nil {
		t.Fatalf("写入安装记录失败：%v", err)
	}

	catalog := NewCatalog(host)
	err := catalog.Launch(key)
	if err == nil || !strings.Contains(err.Error(), "不在原安装位置") {
		t.Fatalf("期望中文失效提示，实际：%v", err)
	}
	if _, ok := host.store.Get(key); ok {
		t.Fatalf("失效记录应被移除")
	}
}
