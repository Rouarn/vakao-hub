//go:build windows

package install

import (
	"archive/zip"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"vakaohub/launcher/internal/contract"
)

// TestWindowsBusyQuitThenInstall 是 Windows 真机验证：
// 目标应用运行中时能被检测到，WM_CLOSE 能优雅退出，随后安装继续并完成，且没有备份残留。
// 需要真实桌面/窗口站，默认跳过，仅在 VAKAOHUB_WIN_E2E=1 且提供 fixture 路径时运行。
func TestWindowsBusyQuitThenInstall(t *testing.T) {
	if os.Getenv("VAKAOHUB_WIN_E2E") != "1" {
		t.Skip("仅在 Windows 真机验证时运行")
	}
	fixture := os.Getenv("VAKAOHUB_WIN_FIXTURE")
	if fixture == "" {
		t.Fatal("未设置 VAKAOHUB_WIN_FIXTURE")
	}

	root := t.TempDir()
	appData := filepath.Join(root, "appdata")
	t.Setenv("APPDATA", appData)
	target := filepath.Join(appData, "VaKaoHub", "apps", "e2e")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("创建目标目录失败：%v", err)
	}
	exe := filepath.Join(target, "E2EDemo.exe")
	if err := copyFile(fixture, exe); err != nil {
		t.Fatalf("准备测试应用失败：%v", err)
	}

	command := exec.Command(exe)
	if err := command.Start(); err != nil {
		t.Fatalf("启动测试应用失败：%v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		processes, err := RunningProcesses(target)
		if err != nil {
			t.Fatalf("检测运行中进程失败：%v", err)
		}
		if len(processes) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("未检测到刚启动的测试应用")
		}
		time.Sleep(100 * time.Millisecond)
	}

	other, err := RunningProcesses(t.TempDir())
	if err != nil {
		t.Fatalf("检测其他目录失败：%v", err)
	}
	if len(other) != 0 {
		t.Fatalf("其他目录不应命中：%+v", other)
	}

	processes, err := RunningProcesses(target)
	if err != nil {
		t.Fatalf("检测运行中进程失败：%v", err)
	}
	started := time.Now()
	if err := QuitProcesses(processes, QuitOptions{Grace: QuitGrace}); err != nil {
		t.Fatalf("优雅退出失败：%v", err)
	}
	if elapsed := time.Since(started); elapsed >= QuitGrace {
		t.Fatalf("应用未在宽限期内正常退出（耗时 %v）", elapsed)
	}
	if processAlive(command.Process.Pid) {
		t.Fatal("测试应用仍在运行")
	}
	_, _ = command.Process.Wait()

	archive := filepath.Join(root, "demo-1.1.0.zip")
	writeArchive(t, archive, fixture)
	if _, err := Install(context.Background(), contract.FormatZIP, archive, "e2e", "E2E Demo", "", ""); err != nil {
		t.Fatalf("退出后继续安装失败：%v", err)
	}

	version, err := os.ReadFile(filepath.Join(target, "version.txt"))
	if err != nil {
		t.Fatalf("安装后未找到新版本标记：%v", err)
	}
	if string(version) != "1.1.0" {
		t.Fatalf("安装后版本标记 = %q", version)
	}
	assertNoBackups(t, filepath.Join(appData, "VaKaoHub", "apps"))
}

func writeArchive(t *testing.T, path, exe string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建安装包失败：%v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	copyInto := func(name, source string) {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", source, err)
		}
		member, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			t.Fatalf("写入压缩包失败：%v", err)
		}
		if _, err := member.Write(data); err != nil {
			t.Fatalf("写入压缩包失败：%v", err)
		}
	}
	copyInto("E2EDemo.exe", exe)

	marker, err := writer.Create("version.txt")
	if err != nil {
		t.Fatalf("写入版本标记失败：%v", err)
	}
	if _, err := marker.Write([]byte("1.1.0")); err != nil {
		t.Fatalf("写入版本标记失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭压缩包失败：%v", err)
	}
}

func copyFile(source, destination string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, data, 0o755)
}

func assertNoBackups(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取安装目录失败：%v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() && (len(entry.Name()) > 0 && (entry.Name()[0] == '.')) {
			t.Fatalf("安装后残留临时目录：%s", entry.Name())
		}
	}
}
