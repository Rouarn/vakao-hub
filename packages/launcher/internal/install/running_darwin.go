//go:build darwin

package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

var psLine = regexp.MustCompile(`^\s*(\d+)\s+(.+?)\s*$`)

// listRunning 通过 ps 读取进程可执行文件路径，匹配目标 .app（或目标文件所在目录）。
func listRunning(target string) ([]RunningProcess, error) {
	// ps 给出的路径有时是真实路径（/tmp、/var 都是符号链接），
	// 有时保留启动时的写法；两种形式都要能匹配。
	bases := candidateBases(target)

	output, err := exec.Command("ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	self := os.Getpid()
	processes := make([]RunningProcess, 0)
	for _, line := range strings.Split(string(output), "\n") {
		match := psLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid == self || pid <= 0 {
			continue
		}
		path := match[2]
		if !withinTarget(path, bases) {
			continue
		}
		processes = append(processes, RunningProcess{PID: pid, Name: filepath.Base(path), Path: path})
	}
	return processes, nil
}

// candidateBases 返回目标目录的原始路径与解析后的真实路径。
func candidateBases(target string) []string {
	paths := []string{target}
	if resolved, err := filepath.EvalSymlinks(target); err == nil && resolved != target {
		paths = append(paths, resolved)
	}
	bases := make([]string, 0, len(paths))
	for _, path := range paths {
		base := path
		if info, err := os.Stat(path); err != nil || !info.IsDir() {
			base = filepath.Dir(path)
		}
		bases = append(bases, strings.TrimSuffix(base, string(filepath.Separator)))
	}
	return dedupe(bases)
}

func withinTarget(path string, bases []string) bool {
	if withinAny(path, bases) {
		return true
	}
	// ps 可能保留启动时的写法，而目标路径可能经过另一条符号链接；
	// 直接匹配失败时再把进程路径解析成真实路径比一次。
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		return withinAny(resolved, bases)
	}
	return false
}

func withinAny(path string, bases []string) bool {
	for _, base := range bases {
		if path == base || strings.HasPrefix(path, base+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// requestQuit 先发 SIGTERM，让应用有机会保存状态后自行退出。
func requestQuit(processes []RunningProcess) error {
	var firstErr error
	for _, process := range processes {
		if err := syscall.Kill(process.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func forceKill(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
