// Package updater 是自更新 helper 与 launcher 共用的执行器；不 import Wails。
package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const JobVersion = 1

const (
	StatusSuccess    = "success"
	StatusRolledBack = "rolled_back"
	StatusFailed     = "failed"
)

// Job 由 launcher 写入文件，helper 读取；路径必须全部为绝对路径。
type Job struct {
	Version            int    `json:"version"`
	ParentPID          int    `json:"parent_pid"`
	Staged             string `json:"staged"`
	Target             string `json:"target"`
	Backup             string `json:"backup"`
	Log                string `json:"log"`
	Result             string `json:"result"`
	WaitTimeoutSeconds int    `json:"wait_timeout_seconds"`
	Relaunch           bool   `json:"relaunch"`
}

// Result 由 helper 写入，下一次启动 launcher 读取并删除。
type Result struct {
	Version   int    `json:"version"`
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	UpdatedAt int64  `json:"updated_at"`
}

func LoadJob(path string) (Job, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Job{}, fmt.Errorf("读取更新任务失败: %w", err)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, fmt.Errorf("解析更新任务失败: %w", err)
	}
	if err := job.Validate(); err != nil {
		return Job{}, err
	}
	return job, nil
}

func WriteJob(path string, job Job) error {
	if err := job.Validate(); err != nil {
		return err
	}
	return writeJSONAtomic(path, job)
}

func (j Job) Validate() error {
	if j.Version != JobVersion {
		return fmt.Errorf("不支持的更新任务版本: %d", j.Version)
	}
	if j.ParentPID <= 0 {
		return errors.New("更新任务缺少父进程 PID")
	}
	paths := map[string]string{
		"staged": j.Staged,
		"target": j.Target,
		"backup": j.Backup,
		"log":    j.Log,
		"result": j.Result,
	}
	for name, value := range paths {
		if strings.TrimSpace(value) == "" || !filepath.IsAbs(value) {
			return fmt.Errorf("更新任务的 %s 必须是绝对路径", name)
		}
	}
	if j.WaitTimeoutSeconds <= 0 || j.WaitTimeoutSeconds > 3600 {
		return fmt.Errorf("等待父进程超时时间无效: %d", j.WaitTimeoutSeconds)
	}
	return nil
}

func WriteResult(path string, result Result) error {
	if !filepath.IsAbs(path) {
		return errors.New("更新结果路径必须是绝对路径")
	}
	result.Version = JobVersion
	return writeJSONAtomic(path, result)
}

func ReadResult(path string) (Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, fmt.Errorf("解析更新结果失败: %w", err)
	}
	return result, nil
}

// CacheDir 返回自更新工作目录。
func CacheDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "vakaohub", "updates"), nil
}

// ResultPath 是下一次启动读取的固定结果路径。
func ResultPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "last-result.json"), nil
}

// LogPath 是 helper 的统一日志路径。
func LogPath() (string, error) {
	dir, err := CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "self-update.log"), nil
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
