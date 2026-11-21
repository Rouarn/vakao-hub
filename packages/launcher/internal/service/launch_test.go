package service

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestLaunchPendingInstallerExplainsWait 安装器刚启动、注册表还没写入时，
// 不能报「位置已失效」，要明确告诉用户安装尚未完成。
func TestLaunchPendingInstallerExplainsWait(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	key := "pending-installer-test"
	name := "Pending Installer Test 不存在"
	if err := host.store.Put(store.Installed{
		AppKey:      key,
		Name:        name,
		Kind:        "installer",
		Pending:     true,
		InstalledAt: time.Now().Unix(),
	}); err != nil {
		t.Fatalf("写入安装记录失败：%v", err)
	}

	catalog := NewCatalog(host)
	err := catalog.Launch(key)
	if err == nil || !strings.Contains(err.Error(), "安装尚未完成") {
		t.Fatalf("期望提示安装未完成，实际：%v", err)
	}
	if _, ok := host.store.Get(key); !ok {
		t.Fatalf("等待中的记录不应被移除")
	}
}

// TestRevealUnknownInstallerLocationRevealsWhy 安装器类路径未知时，
// 不能把空字符串丢给 Reveal，而要引导用户手动指定位置。
func TestRevealUnknownInstallerLocationRevealsWhy(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	key := "reveal-installer-test"
	if err := host.store.Put(store.Installed{
		AppKey:  key,
		Name:    "Reveal Installer Test 不存在",
		Kind:    "installer",
		Pending: true,
	}); err != nil {
		t.Fatalf("写入安装记录失败：%v", err)
	}

	catalog := NewCatalog(host)
	err := catalog.Reveal(key)
	if err == nil || !strings.Contains(err.Error(), "手动指定程序位置") {
		t.Fatalf("期望提示手动指定位置，实际：%v", err)
	}
}
