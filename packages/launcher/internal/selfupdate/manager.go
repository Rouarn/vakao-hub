// Package selfupdate 负责 launcher 侧的自更新准备：解析当前 bundle、暂存新 payload、
// 写 job、解出嵌入的 helper 并脱离进程启动。helper 本身在 internal/updater。
package selfupdate

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/updater"
)

type Manager struct {
	// CurrentBundle 允许测试注入；默认使用 install.CurrentBundle。
	CurrentBundle func() (string, string, bool)
}

type Prepared struct {
	Job        updater.Job
	JobPath    string
	HelperPath string
}

func New() *Manager {
	return &Manager{CurrentBundle: install.CurrentBundle}
}

// PrepareAndStart 是 InstallService 使用的入口。
func (m *Manager) PrepareAndStart(ctx context.Context, format, filePath string) error {
	prepared, err := m.Prepare(ctx, format, filePath)
	if err != nil {
		return err
	}
	return m.Start(prepared)
}

// Prepare 创建 stage、校验 payload、写 job 并解出 helper。
func (m *Manager) Prepare(ctx context.Context, format, filePath string) (Prepared, error) {
	currentBundle := m.CurrentBundle
	if currentBundle == nil {
		currentBundle = install.CurrentBundle
	}
	current, _, ok := currentBundle()
	if !ok {
		return Prepared{}, apperr.New(
			apperr.KindUnsupported,
			"selfupdate",
			"当前不是从安装位置启动，无法自动更新；请手动下载替换",
			nil,
		)
	}
	if !install.IsDirWritable(filepath.Dir(current)) {
		return Prepared{}, apperr.New(
			apperr.KindPermission,
			"selfupdate",
			"没有权限替换启动器，请手动下载安装",
			nil,
		)
	}

	cacheDir, err := updater.CacheDir()
	if err != nil {
		return Prepared{}, apperr.FromOS(err, "selfupdate.cache")
	}
	opID := newOperationID()
	workDir := filepath.Join(cacheDir, "op-"+opID)
	stageDir := filepath.Join(workDir, "stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return Prepared{}, apperr.FromOS(err, "selfupdate.stage")
	}

	staged, err := stagePayload(ctx, format, filePath, stageDir, filepath.Base(current))
	if err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, err
	}
	if err := validatePayload(ctx, staged); err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, err
	}

	logPath, err := updater.LogPath()
	if err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, apperr.FromOS(err, "selfupdate.log")
	}
	resultPath, err := updater.ResultPath()
	if err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, apperr.FromOS(err, "selfupdate.result")
	}

	job := updater.Job{
		Version:            updater.JobVersion,
		ParentPID:          os.Getpid(),
		Staged:             staged,
		Target:             current,
		Backup:             filepath.Join(filepath.Dir(current), "."+filepath.Base(current)+".vakaohub-old-"+opID),
		Log:                logPath,
		Result:             resultPath,
		WaitTimeoutSeconds: 300,
		Relaunch:           true,
	}
	jobPath := filepath.Join(workDir, "job.json")
	if err := updater.WriteJob(jobPath, job); err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, apperr.New(apperr.KindInternal, "selfupdate.job", "写入更新任务失败，请重试", err)
	}

	helperPath, err := extractHelper(workDir)
	if err != nil {
		_ = os.RemoveAll(workDir)
		return Prepared{}, err
	}
	return Prepared{Job: job, JobPath: jobPath, HelperPath: helperPath}, nil
}

// Start 脱离 launcher 进程启动 helper，立即返回；之后进入不可取消阶段。
func (m *Manager) Start(prepared Prepared) error {
	if err := startDetached(prepared.HelperPath, prepared.JobPath); err != nil {
		return apperr.New(apperr.KindInternal, "selfupdate.start", "启动更新程序失败，请手动下载安装", err)
	}
	return nil
}

func newOperationID() string {
	buffer := make([]byte, 6)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}
