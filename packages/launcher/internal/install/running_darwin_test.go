//go:build darwin

package install

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vakaohub/launcher/internal/apperr"
)

func TestRunningProcessesMatchesTargetDirectory(t *testing.T) {
	pid, dir := startResident(t)

	found, err := RunningProcesses(dir)
	if err != nil {
		t.Fatalf("检测运行中进程失败：%v", err)
	}
	if !hasPID(found, pid) {
		t.Fatalf("未检测到目标目录内的进程 %d：%+v", pid, found)
	}

	// ps 输出真实路径；通过符号链接传入目标目录时也必须命中。
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("创建符号链接失败：%v", err)
	}
	linked, err := RunningProcesses(link)
	if err != nil {
		t.Fatalf("通过符号链接检测失败：%v", err)
	}
	if !hasPID(linked, pid) {
		t.Fatalf("符号链接目录未命中进程 %d：%+v", pid, linked)
	}

	other := t.TempDir()
	missing, err := RunningProcesses(other)
	if err != nil {
		t.Fatalf("检测其他目录失败：%v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("其他目录不应命中：%+v", missing)
	}
}

func TestQuitProcessesGracefulExit(t *testing.T) {
	pid, dir := startResident(t)
	processes, err := RunningProcesses(dir)
	if err != nil {
		t.Fatalf("检测运行中进程失败：%v", err)
	}

	var messages []string
	err = QuitProcesses(processes, QuitOptions{
		Grace:  3 * time.Second,
		Report: func(message string) { messages = append(messages, message) },
	})
	if err != nil {
		t.Fatalf("正常退出失败：%v", err)
	}
	if processAlive(pid) {
		t.Fatalf("进程 %d 仍在运行", pid)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "已退出") {
		t.Fatalf("进度未提示退出成功：%v", messages)
	}
}

func TestQuitProcessesForceKillsWhenGraceExpires(t *testing.T) {
	pid, dir := startResident(t)
	processes, err := RunningProcesses(dir)
	if err != nil {
		t.Fatalf("检测运行中进程失败：%v", err)
	}

	originalQuit := requestQuitFn
	originalKill := forceKillFn
	requestQuitFn = func([]RunningProcess) error { return nil }
	killed := 0
	forceKillFn = func(target int) error {
		killed++
		return originalKill(target)
	}
	t.Cleanup(func() {
		requestQuitFn = originalQuit
		forceKillFn = originalKill
	})

	err = QuitProcesses(processes, QuitOptions{
		Grace:     200 * time.Millisecond,
		ForceWait: 3 * time.Second,
	})
	if err != nil {
		t.Fatalf("强杀后应成功：%v", err)
	}
	if killed == 0 {
		t.Fatalf("未调用强制结束")
	}
	if processAlive(pid) {
		t.Fatalf("强杀后进程 %d 仍在运行", pid)
	}
}

func TestQuitProcessesReportsReadableFailure(t *testing.T) {
	_, dir := startResident(t)
	processes, err := RunningProcesses(dir)
	if err != nil {
		t.Fatalf("检测运行中进程失败：%v", err)
	}

	originalQuit := requestQuitFn
	originalKill := forceKillFn
	requestQuitFn = func([]RunningProcess) error { return nil }
	forceKillFn = func(int) error { return os.ErrPermission }
	t.Cleanup(func() {
		requestQuitFn = originalQuit
		forceKillFn = originalKill
	})

	err = QuitProcesses(processes, QuitOptions{
		Grace:     200 * time.Millisecond,
		ForceWait: 300 * time.Millisecond,
	})
	if apperr.KindOf(err) != apperr.KindRunning {
		t.Fatalf("退出失败 Kind = %s，期望 running（%v）", apperr.KindOf(err), err)
	}
	if !strings.Contains(apperr.UserMessage(err), "拒绝访问") {
		t.Fatalf("退出失败未说明原因：%s", apperr.UserMessage(err))
	}
}

// TestResidentHelper 作为子进程运行时会驻留，供父测试检测与退出。
func TestResidentHelper(t *testing.T) {
	if os.Getenv("VAKAOHUB_RESIDENT_HELPER") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
}

// startResident 以测试二进制自身充当目标目录内的驻留进程：
// 可执行文件位于 Go 构建缓存目录，检测按该目录匹配，无需复制或调用外部程序。
func startResident(t *testing.T) (int, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("获取测试可执行文件路径失败：%v", err)
	}
	dir := filepath.Dir(self)

	cmd := exec.Command(self, "-test.run=TestResidentHelper")
	cmd.Env = append(os.Environ(), "VAKAOHUB_RESIDENT_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动驻留进程失败：%v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-done
	})
	waitFor(t, func() bool { return processAlive(cmd.Process.Pid) })
	return cmd.Process.Pid, dir
}

func hasPID(processes []RunningProcess, pid int) bool {
	for _, process := range processes {
		if process.PID == pid {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
