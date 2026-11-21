package updater

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ErrWaitTimeout 表示父进程在约定时间内没有退出。
var ErrWaitTimeout = errors.New("等待父进程退出超时")

// Hooks 是平台差异点：等待父进程、替换 payload、拉起应用、校验 payload。
type Hooks struct {
	WaitForExit func(pid int, timeout time.Duration) error
	Replace     func(staged, target, backup string) (func() error, error)
	Relaunch    func(target string) error
	Validate    func(path string) error
}

// Run 执行六条路径：成功、staged 损坏、父进程超时、替换失败、拉起失败回滚、swap 后校验失败回滚。
func Run(job Job, hooks Hooks) Result {
	logf := newLogger(job.Log)
	result := Result{
		Version:   JobVersion,
		Status:    StatusFailed,
		Message:   "更新失败，请手动下载安装",
		UpdatedAt: time.Now().Unix(),
	}
	fail := func(message string, err error) Result {
		result.Status = StatusFailed
		result.Message = message
		if err != nil {
			result.Error = err.Error()
			logf("失败: %v", err)
		}
		return result
	}

	if hooks.WaitForExit == nil || hooks.Replace == nil || hooks.Relaunch == nil || hooks.Validate == nil {
		return fail("更新组件不完整，请重新安装", errors.New("hooks 未配置"))
	}

	logf("开始更新：pid=%d staged=%s target=%s", job.ParentPID, job.Staged, job.Target)
	timeout := time.Duration(job.WaitTimeoutSeconds) * time.Second
	if err := hooks.WaitForExit(job.ParentPID, timeout); err != nil {
		if errors.Is(err, ErrWaitTimeout) {
			return fail("等待旧进程退出超时，更新已取消", err)
		}
		return fail("等待旧进程退出失败，更新已取消", err)
	}
	logf("父进程已退出")

	if err := hooks.Validate(job.Staged); err != nil {
		relaunchOld(job, hooks, logf)
		return fail("更新包校验失败，已保留旧版本", err)
	}
	logf("暂存包校验通过")

	rollback, err := hooks.Replace(job.Staged, job.Target, job.Backup)
	if err != nil {
		relaunchOld(job, hooks, logf)
		return fail("替换启动器失败，已保留旧版本", err)
	}
	logf("payload 已换入目标位置")

	if err := hooks.Validate(job.Target); err != nil {
		if rollbackErr := rollback(); rollbackErr != nil {
			return fail("更新后校验失败且回滚失败，请手动重新安装", errors.Join(err, rollbackErr))
		}
		logf("swap 后校验失败，已回滚: %v", err)
		relaunchOld(job, hooks, logf)
		result.Status = StatusRolledBack
		result.Message = "更新包校验失败，已恢复到旧版本"
		result.Error = err.Error()
		return result
	}

	if job.Relaunch {
		if err := hooks.Relaunch(job.Target); err != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return fail("新版本启动失败且回滚失败，请手动重新安装", errors.Join(err, rollbackErr))
			}
			logf("新版本拉起失败，已回滚: %v", err)
			relaunchOld(job, hooks, logf)
			result.Status = StatusRolledBack
			result.Message = "新版本启动失败，已恢复到旧版本"
			result.Error = err.Error()
			return result
		}
	}

	if err := os.RemoveAll(job.Backup); err != nil {
		logf("清理旧版本备份失败（不影响更新）: %v", err)
	}
	logf("更新成功")
	result.Status = StatusSuccess
	result.Message = "启动器已更新到新版本"
	result.Error = ""
	return result
}

func relaunchOld(job Job, hooks Hooks, logf func(string, ...any)) {
	if !job.Relaunch {
		return
	}
	if err := hooks.Relaunch(job.Target); err != nil {
		logf("重新拉起旧版本失败: %v", err)
	}
}

func newLogger(path string) func(string, ...any) {
	if path == "" {
		return func(format string, args ...any) { log.Printf(format, args...) }
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return func(format string, args ...any) { log.Printf(format, args...) }
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return func(format string, args ...any) { log.Printf(format, args...) }
	}
	logger := log.New(file, "", log.LstdFlags)
	return func(format string, args ...any) {
		logger.Printf(format, args...)
	}
}
