//go:build darwin

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestSelfUpdateScriptReplacesBundle 覆盖自更新最关键的一段：旧进程退出后，
// 新 bundle 顶替目标路径、备份与缓存被清理，并用新版本重新打开。
// ditto/open 用 PATH 上的假命令替代，避免测试真的去启动图形应用。
func TestSelfUpdateScriptReplacesBundle(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Apps", "VaKaoHub.app")
	staged := filepath.Join(root, "stage", "VaKaoHub.app")
	backup := target + ".old"
	openLog := filepath.Join(root, "opened.log")

	writeTestFile(t, filepath.Join(target, "version"), "1.0.0")
	writeTestFile(t, filepath.Join(staged, "version"), "1.0.1")

	bin := filepath.Join(root, "bin")
	writeTestExecutable(t, filepath.Join(bin, "open"), "#!/bin/sh\necho \"$1\" > "+openLog+"\n")
	writeTestExecutable(t, filepath.Join(bin, "ditto"), "#!/bin/sh\ncp -R \"$1\" \"$2\"\n")

	script := filepath.Join(root, "self-update.sh")
	if err := os.WriteFile(script, []byte(selfUpdateScript()), 0o700); err != nil {
		t.Fatalf("写入脚本失败：%v", err)
	}

	// 用 0.3 秒后被杀掉的进程充当“正在运行的旧启动器”。
	// 必须在脚本运行期间把它回收，否则僵尸进程让 kill -0 一直成功，脚本会等满 5 分钟。
	sleeper := exec.Command("sleep", "5")
	if err := sleeper.Start(); err != nil {
		t.Fatalf("启动模拟进程失败：%v", err)
	}
	pid := sleeper.Process.Pid
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
	}()

	command := exec.Command("/bin/sh", script, strconv.Itoa(pid), staged, target, backup)
	command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("脚本执行失败：%v %s", err, output)
	}

	if got := readTestFile(t, filepath.Join(target, "version")); got != "1.0.1" {
		t.Fatalf("目标 bundle 版本 = %q，期望 1.0.1", got)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("备份目录应被清理，实际 err=%v", err)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Fatalf("解压缓存应被清理，实际 err=%v", err)
	}
	if got := readTestFile(t, openLog); got != target+"\n" {
		t.Fatalf("重新打开的路径 = %q，期望 %q", got, target)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入 %s 失败：%v", path, err)
	}
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	writeTestFile(t, path, content)
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("设置可执行权限失败：%v", err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return string(data)
}
