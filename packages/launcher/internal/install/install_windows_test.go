//go:build windows

package install

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"vakaohub/launcher/internal/contract"
)

// TestDefaultInstallDirUsesLocalAppData 默认落点必须在 Local 而不是 Roaming，
// 避免可执行文件被漫游配置同步；%LOCALAPPDATA% 缺失时才回落旧目录。
func TestDefaultInstallDirUsesLocalAppData(t *testing.T) {
	local := filepath.Join(t.TempDir(), "Local")
	t.Setenv("LOCALAPPDATA", local)
	want := filepath.Join(local, "Programs", "VaKaoHub")
	if got := DefaultInstallDir(); got != want {
		t.Fatalf("DefaultInstallDir() = %s，期望 %s", got, want)
	}
}

func TestValidateInstallDirRejectsUnsafeTargets(t *testing.T) {
	if err := ValidateInstallDir(""); err != nil {
		t.Fatalf("空串表示默认目录，不应报错：%v", err)
	}
	if err := ValidateInstallDir("relative\\dir"); err == nil {
		t.Fatal("相对路径应被拒绝")
	}
	if err := ValidateInstallDir(`C:\`); err == nil {
		t.Fatal("盘符根目录应被拒绝")
	}
	if err := ValidateInstallDir(os.Getenv("SystemRoot")); err == nil {
		t.Fatal("系统目录应被拒绝")
	}
	if err := ValidateInstallDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("不存在的目录应被拒绝")
	}

	ok := filepath.Join(t.TempDir(), "VaKaoHub")
	if err := os.MkdirAll(ok, 0o755); err != nil {
		t.Fatalf("创建测试目录失败：%v", err)
	}
	if err := ValidateInstallDir(ok); err != nil {
		t.Fatalf("固定盘上的可写目录应通过：%v", err)
	}
}

// TestResolveContainerKeepsExistingLocation 升级必须留在原容器，
// 否则用户在设置里换目录后，一次更新就会把应用搬家。
func TestResolveContainerKeepsExistingLocation(t *testing.T) {
	oldRoot := filepath.Join(t.TempDir(), "old")
	existing := filepath.Join(oldRoot, "demo", "app.exe")
	newRoot := filepath.Join(t.TempDir(), "new")

	if got := resolveContainer("demo", existing, newRoot); got != filepath.Join(oldRoot, "demo") {
		t.Fatalf("升级应留在原容器，实际 %s", got)
	}
	if got := resolveContainer("demo", "", newRoot); got != filepath.Join(newRoot, "demo") {
		t.Fatalf("新安装应使用配置目录，实际 %s", got)
	}
}

func TestInstallZipUsesConfiguredDirAndKeepsUpdatesInPlace(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "app.zip")
	writeTestZip(t, archive, "app.exe", []byte("MZ fake exe"))

	firstRoot := filepath.Join(root, "first")
	secondRoot := filepath.Join(root, "second")
	if err := os.MkdirAll(secondRoot, 0o755); err != nil {
		t.Fatalf("创建第二个目录失败：%v", err)
	}

	first, err := Install(context.Background(), Options{
		Format: contract.FormatZIP, FilePath: archive, AppKey: "demo", AppName: "Demo", InstallDir: firstRoot,
	})
	if err != nil {
		t.Fatalf("首次安装失败：%v", err)
	}
	want := filepath.Join(firstRoot, "demo", "app.exe")
	if first.Path != want {
		t.Fatalf("安装路径 = %s，期望 %s", first.Path, want)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("安装文件不存在：%v", err)
	}

	// 配置目录换成 secondRoot，但升级仍应写回 firstRoot。
	upgraded, err := Install(context.Background(), Options{
		Format: contract.FormatZIP, FilePath: archive, AppKey: "demo", AppName: "Demo",
		TargetPath: first.Path, InstallDir: secondRoot,
	})
	if err != nil {
		t.Fatalf("升级失败：%v", err)
	}
	if upgraded.Path != want {
		t.Fatalf("升级路径 = %s，期望留在原位置 %s", upgraded.Path, want)
	}
	if _, err := os.Stat(filepath.Join(secondRoot, "demo")); !os.IsNotExist(err) {
		t.Fatalf("升级不应在新目录留下副本：%v", err)
	}
}

func TestRemoveDeletesContainerOnly(t *testing.T) {
	root := t.TempDir()
	container := filepath.Join(root, "demo")
	if err := os.MkdirAll(container, 0o755); err != nil {
		t.Fatalf("创建容器失败：%v", err)
	}
	exe := filepath.Join(container, "app.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
	if err := Remove(exe, "demo"); err != nil {
		t.Fatalf("卸载失败：%v", err)
	}
	if _, err := os.Stat(container); !os.IsNotExist(err) {
		t.Fatalf("容器目录应被删除：%v", err)
	}

	// appKey 不匹配时必须拒绝，避免误删同级其它内容。
	other := filepath.Join(root, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	otherExe := filepath.Join(other, "app.exe")
	if err := os.WriteFile(otherExe, []byte("MZ"), 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
	if err := Remove(otherExe, "demo"); err == nil {
		t.Fatal("appKey 不匹配应拒绝删除")
	}
	if _, err := os.Stat(otherExe); err != nil {
		t.Fatalf("拒绝删除后文件应保留：%v", err)
	}
}

func TestClassifySyntheticSignals(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("写入样本失败：%v", err)
		}
		return path
	}

	// 便携框架标记优先于「名字像安装程序」。
	portable := write("setup.exe", "MZ....Go build ID....PE")
	if got := Classify(contract.FormatEXE, portable); got.Kind != KindPortable || got.Confidence != ConfidenceCertain {
		t.Fatalf("Go 单文件应判为确定便携，实际 %+v", got)
	}

	// 安装器签名优先于便携标记。
	installer := write("setup.exe", "MZ....NullsoftInst....PE")
	if got := Classify(contract.FormatEXE, installer); got.Kind != KindInstaller {
		t.Fatalf("NSIS 标记应判为安装器，实际 %+v", got)
	}

	// 三个信号都没有时落到不确定，交给用户二选一。
	unknown := write("demo.exe", "MZ....ordinary application....PE")
	if got := Classify(contract.FormatEXE, unknown); got.Kind != KindPortable || got.Confidence != ConfidenceUncertain {
		t.Fatalf("未知文件应判为不确定便携，实际 %+v", got)
	}

	// 文件名自带 setup 时不需要弹窗，直接按安装器处理。
	named := write("MyAppSetup.exe", "MZ....ordinary application....PE")
	if got := Classify(contract.FormatEXE, named); got.Kind != KindInstaller {
		t.Fatalf("setup 文件名应判为安装器，实际 %+v", got)
	}
}

func writeTestZip(t *testing.T, path, name string, data []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 zip 失败：%v", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatalf("写入 zip 条目失败：%v", err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatalf("写入 zip 内容失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}
}
