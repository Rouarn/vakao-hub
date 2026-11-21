package install

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
)

// QuitGrace 是请求应用正常退出后的宽限时长。
// 5 秒足够大多数桌面应用保存状态并释放文件句柄；再长会让用户以为点击没反应，
// 再短则容易把本来能正常退出的应用直接强杀。
const QuitGrace = 5 * time.Second

// QuitForceWait 是强制结束进程后等待其真正消失的上限。
const QuitForceWait = 2 * time.Second

// RunningProcess 描述一个占用目标安装目录的进程。
type RunningProcess struct {
	PID  int
	Name string
	Path string
}

// QuitOptions 控制退出流程；零值使用默认宽限与等待时长。
type QuitOptions struct {
	Grace     time.Duration
	ForceWait time.Duration
	Report    func(message string)
}

// 允许测试替换，避免真的对系统进程发信号。
var (
	requestQuitFn  = requestQuit
	processAliveFn = processAlive
	forceKillFn    = forceKill
)

// RunningProcesses 返回可执行文件位于 target 内（target 为文件时取所在目录）的进程。
func RunningProcesses(target string) ([]RunningProcess, error) {
	if strings.TrimSpace(target) == "" {
		return nil, nil
	}
	return listRunning(target)
}

// QuitProcesses 先请求正常退出，超过宽限期后强制结束；任何未退出的进程都会形成可读错误。
func QuitProcesses(processes []RunningProcess, options QuitOptions) error {
	if len(processes) == 0 {
		return nil
	}
	grace := options.Grace
	if grace <= 0 {
		grace = QuitGrace
	}
	forceWait := options.ForceWait
	if forceWait <= 0 {
		forceWait = QuitForceWait
	}
	report := options.Report
	if report == nil {
		report = func(string) {}
	}

	report(fmt.Sprintf("正在请求退出 %s（共 %d 个进程）", processNames(processes), len(processes)))
	_ = requestQuitFn(processes)
	if waitGone(processes, grace, report) {
		report(processNames(processes) + " 已退出，继续安装")
		return nil
	}

	remaining := aliveProcesses(processes)
	failures := make([]string, 0, len(remaining))
	for _, process := range remaining {
		report(fmt.Sprintf("「%s」（PID %d）未响应退出请求，正在强制结束", process.Name, process.PID))
		if err := forceKillFn(process.PID); err != nil {
			failures = append(failures, fmt.Sprintf("「%s」（PID %d）%s", process.Name, process.PID, readableReason(err)))
		}
	}
	if waitGone(processes, forceWait, report) {
		report(processNames(processes) + " 已强制结束，继续安装")
		return nil
	}

	for _, process := range aliveProcesses(processes) {
		failures = append(failures, fmt.Sprintf("「%s」（PID %d）未能在限期内退出", process.Name, process.PID))
	}
	return apperr.New(
		apperr.KindRunning,
		"install.quit",
		fmt.Sprintf("无法退出 %s：%s", processNames(processes), strings.Join(dedupe(failures), "；")),
		nil,
	)
}

func waitGone(processes []RunningProcess, timeout time.Duration, report func(string)) bool {
	deadline := time.Now().Add(timeout)
	lastRemaining := len(processes)
	for {
		remaining := aliveProcesses(processes)
		if len(remaining) == 0 {
			return true
		}
		if len(remaining) != lastRemaining {
			lastRemaining = len(remaining)
			report(fmt.Sprintf("等待应用退出，剩余 %d 个进程", len(remaining)))
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func aliveProcesses(processes []RunningProcess) []RunningProcess {
	alive := make([]RunningProcess, 0, len(processes))
	for _, process := range processes {
		if processAliveFn(process.PID) {
			alive = append(alive, process)
		}
	}
	return alive
}

func processNames(processes []RunningProcess) string {
	if len(processes) == 1 {
		return "「" + processes[0].Name + "」"
	}
	names := make([]string, 0, len(processes))
	for _, process := range processes {
		names = append(names, "「"+process.Name+"」")
	}
	return strings.Join(dedupe(names), "、")
}

func readableReason(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, os.ErrPermission) {
		return "拒绝访问，可能以管理员身份运行，请手动退出"
	}
	return err.Error()
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
