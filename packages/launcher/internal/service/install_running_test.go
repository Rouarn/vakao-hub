//go:build darwin

package service

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"vakaohub/launcher/internal/install"
)

// TestCheckRunningEmitsWarningWhenDetectionFails 验证检测失败不阻断安装，但会发出可见告警。
func TestCheckRunningEmitsWarningWhenDetectionFails(t *testing.T) {
	originalDetect := runningProcesses
	runningProcesses = func(string) ([]install.RunningProcess, error) {
		return nil, errors.New("ps 不可用")
	}
	t.Cleanup(func() { runningProcesses = originalDetect })

	originalEmit := emitProgress
	var captured []Progress
	emitProgress = func(_ *application.App, payload Progress) {
		captured = append(captured, payload)
	}
	t.Cleanup(func() { emitProgress = originalEmit })

	svc := &InstallService{host: &Host{}}
	processes := svc.checkRunning("demo", "op-1", "Demo", "/tmp/demo")
	if len(processes) != 0 {
		t.Fatalf("检测失败时应继续安装，进程列表应为空：%+v", processes)
	}
	if len(captured) != 1 {
		t.Fatalf("应发出且仅发出一条告警，实际 %d 条", len(captured))
	}
	warning := captured[0]
	if warning.Phase != "warning" || warning.AppKey != "demo" {
		t.Fatalf("告警事件不正确：%+v", warning)
	}
	if !strings.Contains(warning.Message, "无法确认「Demo」是否正在运行") ||
		!strings.Contains(warning.Message, "已直接继续安装") {
		t.Fatalf("告警文案不完整：%s", warning.Message)
	}

	captured = nil
	svc.checkRunning(selfAppKey, "op-2", "VaKao Hub", "/tmp/self")
	if len(captured) != 0 {
		t.Fatalf("自更新路径不应发告警：%+v", captured)
	}
}

// TestDetectRunningSkipsSelfUpdate 验证自更新路径不会被「应用正在运行」检测拦截。
func TestDetectRunningSkipsSelfUpdate(t *testing.T) {
	pid, dir := startResident(t)
	svc := NewInstall(nil)

	skipped, err := svc.detectRunning(selfAppKey, dir)
	if err != nil {
		t.Fatalf("自更新检测返回错误：%v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("自更新不应被拦截：%+v", skipped)
	}

	detected, err := svc.detectRunning("demo", dir)
	if err != nil {
		t.Fatalf("普通应用检测失败：%v", err)
	}
	found := false
	for _, process := range detected {
		if process.PID == pid {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("普通应用应命中进程 %d：%+v", pid, detected)
	}
}

// TestSelfUpdateResidentHelper 作为子进程运行时会驻留，供父测试检测。
func TestSelfUpdateResidentHelper(t *testing.T) {
	if os.Getenv("VAKAOHUB_SELF_RESIDENT_HELPER") != "1" {
		return
	}
	time.Sleep(60 * time.Second)
}

func startResident(t *testing.T) (int, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("获取测试可执行文件路径失败：%v", err)
	}
	cmd := exec.Command(self, "-test.run=TestSelfUpdateResidentHelper")
	cmd.Env = append(os.Environ(), "VAKAOHUB_SELF_RESIDENT_HELPER=1")
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(cmd.Process.Pid, 0) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cmd.Process.Pid, filepath.Dir(self)
}
