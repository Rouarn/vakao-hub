package service

import (
	"os"
	"path/filepath"
	"testing"

	"vakaohub/launcher/internal/updater"
)

// TestConsumeUpdateNotice 验证上次自更新结果只返回一次：读取后文件被删除。
func TestConsumeUpdateNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := updater.ResultPath()
	if err != nil {
		t.Fatalf("解析结果路径失败：%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建缓存目录失败：%v", err)
	}
	if err := updater.WriteResult(path, updater.Result{
		Status:  updater.StatusSuccess,
		Message: "启动器已更新到新版本",
	}); err != nil {
		t.Fatalf("写入结果失败：%v", err)
	}

	service := &LauncherService{}
	notice := service.ConsumeUpdateNotice()
	if notice == nil {
		t.Fatal("首次读取应返回提示")
	}
	if notice.Message != "启动器已更新到新版本" || notice.Status != updater.StatusSuccess {
		t.Fatalf("提示内容不正确：%+v", notice)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("读取后结果文件应被删除，err=%v", err)
	}
	if second := service.ConsumeUpdateNotice(); second != nil {
		t.Fatalf("第二次读取应返回 nil，实际 %+v", second)
	}
}

// TestConsumeUpdateNoticeMissingFile 没有结果文件时静默返回 nil。
func TestConsumeUpdateNoticeMissingFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	service := &LauncherService{}
	if notice := service.ConsumeUpdateNotice(); notice != nil {
		t.Fatalf("没有结果文件时应返回 nil，实际 %+v", notice)
	}
}
