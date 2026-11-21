//go:build darwin

package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

func TestValidateInstallDirReadOnly(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	dir := homeTempDir(t)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("设置只读权限失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := ValidateInstallDir(dir)
	if apperr.KindOf(err) != apperr.KindPermission {
		t.Fatalf("只读目录 Kind = %s，期望 permission（%v）", apperr.KindOf(err), err)
	}
}

func TestExtractAppCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := ExtractApp(ctx, contract.FormatZIP, "missing.zip", t.TempDir())
	if apperr.KindOf(err) != apperr.KindCanceled {
		t.Fatalf("取消解压 Kind = %s，期望 canceled（%v）", apperr.KindOf(err), err)
	}
}

func TestExtractAppCorruptZip(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "broken.zip")
	if err := os.WriteFile(archive, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("写入坏包失败：%v", err)
	}

	_, err := ExtractApp(context.Background(), contract.FormatZIP, archive, filepath.Join(root, "out"))
	if apperr.KindOf(err) != apperr.KindCorrupt {
		t.Fatalf("坏包 Kind = %s，期望 corrupt（%v）", apperr.KindOf(err), err)
	}
}

func TestInstallUnsupportedFormat(t *testing.T) {
	_, err := Install(context.Background(), contract.FormatAPK, "app.apk", "demo", "Demo", "", "")
	if apperr.KindOf(err) != apperr.KindUnsupported {
		t.Fatalf("不支持格式 Kind = %s，期望 unsupported（%v）", apperr.KindOf(err), err)
	}
}

func TestAtomicInstallFresh(t *testing.T) {
	root := homeTempDir(t)
	installDir := filepath.Join(root, "Applications")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("创建安装目录失败：%v", err)
	}

	archive := makeZip(t, root, "1.1.0")
	installed, err := Install(context.Background(), contract.FormatZIP, archive, "demo", "Demo", "", installDir)
	if err != nil {
		t.Fatalf("原子安装失败：%v", err)
	}
	if installed != filepath.Join(installDir, "Demo.app") {
		t.Fatalf("安装路径 = %s", installed)
	}
	if version := bundleVersion(t, installed); version != "1.1.0" {
		t.Fatalf("安装后版本 = %s", version)
	}
	assertNoAtomicResidue(t, installDir)
}

func TestAtomicInstallUpgradesExisting(t *testing.T) {
	root := homeTempDir(t)
	installDir := filepath.Join(root, "Applications")
	oldApp := filepath.Join(installDir, "Demo.app")
	makeBundle(t, oldApp, "1.0.0")
	signBundle(t, oldApp)

	archive := makeZip(t, root, "1.1.0")
	installed, err := Install(context.Background(), contract.FormatZIP, archive, "demo", "Demo", oldApp, installDir)
	if err != nil {
		t.Fatalf("升级安装失败：%v", err)
	}
	if installed != oldApp {
		t.Fatalf("升级后路径 = %s，期望 %s", installed, oldApp)
	}
	if version := bundleVersion(t, installed); version != "1.1.0" {
		t.Fatalf("升级后版本 = %s", version)
	}
	assertNoAtomicResidue(t, installDir)
}

func TestInstallSwapFailureKeepsOldApp(t *testing.T) {
	root := homeTempDir(t)
	installDir := filepath.Join(root, "Applications")
	oldApp := filepath.Join(installDir, "Demo.app")
	makeBundle(t, oldApp, "1.0.0")
	signBundle(t, oldApp)

	archive := makeZip(t, root, "1.1.0")
	original := swapDirectory
	swapDirectory = func(staged, target string) (func() error, error) {
		return nil, apperr.New(apperr.KindInternal, "test.swap", "注入的 swap 失败", nil)
	}
	t.Cleanup(func() { swapDirectory = original })

	_, err := Install(context.Background(), contract.FormatZIP, archive, "demo", "Demo", oldApp, installDir)
	if err == nil {
		t.Fatal("swap 注入失败时应返回错误")
	}
	if version := bundleVersion(t, oldApp); version != "1.0.0" {
		t.Fatalf("swap 失败后旧版本 = %s，期望 1.0.0", version)
	}
	assertNoAtomicResidue(t, installDir)
}

func TestInstallCorruptZipKeepsOldApp(t *testing.T) {
	root := homeTempDir(t)
	installDir := filepath.Join(root, "Applications")
	oldApp := filepath.Join(installDir, "Demo.app")
	makeBundle(t, oldApp, "1.0.0")

	archive := filepath.Join(root, "broken.zip")
	if err := os.WriteFile(archive, []byte("not a zip"), 0o644); err != nil {
		t.Fatalf("写入坏包失败：%v", err)
	}

	_, err := Install(context.Background(), contract.FormatZIP, archive, "demo", "Demo", oldApp, installDir)
	if apperr.KindOf(err) != apperr.KindCorrupt {
		t.Fatalf("坏包 Kind = %s，期望 corrupt（%v）", apperr.KindOf(err), err)
	}
	if version := bundleVersion(t, oldApp); version != "1.0.0" {
		t.Fatalf("坏包安装后旧版本 = %s，期望 1.0.0", version)
	}
	assertNoAtomicResidue(t, installDir)
}

func TestInstallReadOnlyParentKeepsOldApp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	root := homeTempDir(t)
	installDir := filepath.Join(root, "Applications")
	oldApp := filepath.Join(installDir, "Demo.app")
	makeBundle(t, oldApp, "1.0.0")
	archive := makeZip(t, root, "1.1.0")

	if err := os.Chmod(installDir, 0o500); err != nil {
		t.Fatalf("设置只读权限失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(installDir, 0o755) })

	_, err := Install(context.Background(), contract.FormatZIP, archive, "demo", "Demo", oldApp, installDir)
	if apperr.KindOf(err) != apperr.KindPermission {
		t.Fatalf("只读父目录 Kind = %s，期望 permission（%v）", apperr.KindOf(err), err)
	}
	if version := bundleVersion(t, oldApp); version != "1.0.0" {
		t.Fatalf("只读目录安装失败后旧版本 = %s，期望 1.0.0", version)
	}
}

func homeTempDir(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("获取用户目录失败：%v", err)
	}
	dir, err := os.MkdirTemp(home, ".vakaohub-atomic-test-*")
	if err != nil {
		t.Fatalf("创建测试目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func makeBundle(t *testing.T, path, version string) {
	t.Helper()
	contents := filepath.Join(path, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755); err != nil {
		t.Fatalf("创建 bundle 目录失败：%v", err)
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleExecutable</key><string>Demo</string>
  <key>CFBundleIdentifier</key><string>com.vakaohub.demo</string>
  <key>CFBundleName</key><string>Demo</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>%s</string>
  <key>CFBundleVersion</key><string>%s</string>
</dict>
</plist>`, version, version)
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatalf("写入 Info.plist 失败：%v", err)
	}
	binary, err := os.ReadFile("/bin/echo")
	if err != nil {
		t.Fatalf("读取测试可执行文件失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", "Demo"), binary, 0o755); err != nil {
		t.Fatalf("写入可执行文件失败：%v", err)
	}
}

func signBundle(t *testing.T, path string) {
	t.Helper()
	if output, err := exec.Command("codesign", "--force", "--deep", "--sign", "-", path).CombinedOutput(); err != nil {
		t.Fatalf("签名测试 bundle 失败：%v\n%s", err, output)
	}
}

func makeZip(t *testing.T, root, version string) string {
	t.Helper()
	source := filepath.Join(root, "new-"+version, "Demo.app")
	makeBundle(t, source, version)
	signBundle(t, source)
	archive := filepath.Join(root, "demo-"+version+".zip")
	if output, err := exec.Command(
		"ditto", "-c", "-k", "--sequesterRsrc", "--keepParent", source, archive,
	).CombinedOutput(); err != nil {
		t.Fatalf("打包测试 zip 失败：%v\n%s", err, output)
	}
	return archive
}

func bundleVersion(t *testing.T, path string) string {
	t.Helper()
	output, err := exec.Command(
		"plutil", "-extract", "CFBundleShortVersionString", "raw", "-o", "-",
		filepath.Join(path, "Contents", "Info.plist"),
	).Output()
	if err != nil {
		t.Fatalf("读取 bundle 版本失败：%v", err)
	}
	return strings.TrimSpace(string(output))
}

func assertNoAtomicResidue(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取安装目录失败：%v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".vakaohub-stage-") || strings.Contains(entry.Name(), ".vakaohub-old-") {
			t.Fatalf("残留原子安装目录：%s", entry.Name())
		}
	}
}
