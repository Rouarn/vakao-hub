package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"vakaohub/launcher/internal/apperr"
)

func assertExtracted(t *testing.T, assets fstest.MapFS, goos, goarch, wantBase, wantBody string) string {
	t.Helper()
	dir := t.TempDir()
	path, err := extractHelperFrom(assets, goos, goarch, dir)
	if err != nil {
		t.Fatalf("解出 helper 失败：%v", err)
	}
	if got := filepath.Base(path); got != wantBase {
		t.Fatalf("落盘文件名 = %q，期望 %q", got, wantBase)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 helper 失败：%v", err)
	}
	if string(data) != wantBody {
		t.Fatalf("helper 内容 = %q，期望 %q", data, wantBody)
	}
	return path
}

// TestExtractHelperCanonicalName 验证规范名（Windows 带 .exe）能被正常解出。
func TestExtractHelperCanonicalName(t *testing.T) {
	assets := fstest.MapFS{
		"bin/windows_amd64/vakaohub-updater.exe": &fstest.MapFile{Data: []byte("MZ-canonical")},
	}
	assertExtracted(t, assets, "windows", "amd64", "vakaohub-updater.exe", "MZ-canonical")
}

// TestExtractHelperFallsBackToSuffixless 验证历史交叉编译产物（缺 .exe）仍可用，
// 且落盘时补回 .exe——Windows 的 CreateProcess 不能直接执行无后缀文件。
func TestExtractHelperFallsBackToSuffixless(t *testing.T) {
	assets := fstest.MapFS{
		"bin/windows_amd64/vakaohub-updater": &fstest.MapFile{Data: []byte("MZ-legacy")},
	}
	assertExtracted(t, assets, "windows", "amd64", "vakaohub-updater.exe", "MZ-legacy")
}

// TestExtractHelperDarwinName 验证 macOS 不带后缀。
func TestExtractHelperDarwinName(t *testing.T) {
	assets := fstest.MapFS{
		"bin/darwin_arm64/vakaohub-updater": &fstest.MapFile{Data: []byte("macho")},
	}
	assertExtracted(t, assets, "darwin", "arm64", "vakaohub-updater", "macho")
}

// TestExtractHelperMissing 验证缺失时给出可识别的「更新组件缺失」错误。
func TestExtractHelperMissing(t *testing.T) {
	_, err := extractHelperFrom(fstest.MapFS{}, "windows", "amd64", t.TempDir())
	if err == nil {
		t.Fatal("缺失 helper 时应返回错误")
	}
	if apperr.KindOf(err) != apperr.KindInternal {
		t.Fatalf("错误分类 = %s，期望 internal", apperr.KindOf(err))
	}
	if message := apperr.UserMessage(err); message != "更新组件缺失，请重新安装" {
		t.Fatalf("错误文案 = %q", message)
	}
}

func TestHelperNameFor(t *testing.T) {
	cases := []struct {
		goos, goarch, want string
	}{
		{"windows", "amd64", "windows_amd64/vakaohub-updater.exe"},
		{"windows", "arm64", "windows_arm64/vakaohub-updater.exe"},
		{"darwin", "arm64", "darwin_arm64/vakaohub-updater"},
		{"linux", "amd64", "linux_amd64/vakaohub-updater"},
	}
	for _, item := range cases {
		if got := helperNameFor(item.goos, item.goarch); got != item.want {
			t.Fatalf("helperNameFor(%s,%s) = %q，期望 %q", item.goos, item.goarch, got, item.want)
		}
	}
}
