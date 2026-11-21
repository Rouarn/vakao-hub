package updater

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vakaohub/launcher/internal/atomicfs"
)

// fixture 构造一组真实目录：旧版本在 target，新版本在 staged，backup 在 target 同级。
type fixture struct {
	job    Job
	target string
	staged string
	backup string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	target := filepath.Join(root, "Apps", "VaKaoHub.app")
	staged := filepath.Join(root, "stage", "VaKaoHub.app")
	backup := filepath.Join(root, "Apps", ".VaKaoHub.app.vakaohub-old-test")
	writeVersion(t, target, "1.0.0")
	writeVersion(t, staged, "1.0.1")

	job := Job{
		Version:            JobVersion,
		ParentPID:          os.Getpid(),
		Staged:             staged,
		Target:             target,
		Backup:             backup,
		Log:                filepath.Join(root, "self-update.log"),
		Result:             filepath.Join(root, "last-result.json"),
		WaitTimeoutSeconds: 60,
		Relaunch:           true,
	}
	return fixture{job: job, target: target, staged: staged, backup: backup}
}

func writeVersion(t *testing.T, dir, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "version"), []byte(version), 0o644); err != nil {
		t.Fatalf("写入版本失败：%v", err)
	}
}

func readVersion(t *testing.T, dir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "version"))
	if err != nil {
		t.Fatalf("读取版本失败：%v", err)
	}
	return string(data)
}

// baseHooks 提供真实替换，拉起与校验用测试替身记录调用。
func baseHooks(t *testing.T, relaunched *[]string) Hooks {
	t.Helper()
	return Hooks{
		WaitForExit: func(_ int, _ time.Duration) error { return nil },
		Replace:     atomicfs.SwapWithBackup,
		Relaunch: func(target string) error {
			*relaunched = append(*relaunched, target)
			_, err := os.Stat(filepath.Join(target, "version"))
			return err
		},
		Validate: func(path string) error {
			if _, err := os.Stat(filepath.Join(path, "version")); err != nil {
				return err
			}
			return nil
		},
	}
}

func TestRunSuccessReplacesTarget(t *testing.T) {
	f := newFixture(t)
	var relaunched []string
	result := Run(f.job, baseHooks(t, &relaunched))

	if result.Status != StatusSuccess {
		t.Fatalf("状态 = %q，期望 success；error=%s", result.Status, result.Error)
	}
	if got := readVersion(t, f.target); got != "1.0.1" {
		t.Fatalf("目标版本 = %q，期望 1.0.1", got)
	}
	if _, err := os.Stat(f.backup); !os.IsNotExist(err) {
		t.Fatalf("备份应被清理，err=%v", err)
	}
	if len(relaunched) != 1 || relaunched[0] != f.target {
		t.Fatalf("拉起记录 = %v，期望仅新版本一次", relaunched)
	}
}

func TestRunCorruptStagedKeepsOldVersion(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.staged); err != nil {
		t.Fatalf("删除暂存目录失败：%v", err)
	}
	var relaunched []string
	result := Run(f.job, baseHooks(t, &relaunched))

	if result.Status != StatusFailed {
		t.Fatalf("状态 = %q，期望 failed", result.Status)
	}
	if got := readVersion(t, f.target); got != "1.0.0" {
		t.Fatalf("目标版本 = %q，期望保留 1.0.0", got)
	}
	if len(relaunched) != 1 {
		t.Fatalf("应重新拉起旧版本一次，实际 %v", relaunched)
	}
}

func TestRunWaitTimeoutDoesNotTouchTarget(t *testing.T) {
	f := newFixture(t)
	hooks := baseHooks(t, new([]string))
	hooks.WaitForExit = func(_ int, _ time.Duration) error { return ErrWaitTimeout }
	result := Run(f.job, hooks)

	if result.Status != StatusFailed {
		t.Fatalf("状态 = %q，期望 failed", result.Status)
	}
	if got := readVersion(t, f.target); got != "1.0.0" {
		t.Fatalf("超时后不应替换，目标版本 = %q", got)
	}
}

func TestRunReplaceFailureKeepsOldVersion(t *testing.T) {
	f := newFixture(t)
	var relaunched []string
	hooks := baseHooks(t, &relaunched)
	hooks.Replace = func(_, _, _ string) (func() error, error) {
		return nil, errors.New("rename 失败")
	}
	result := Run(f.job, hooks)

	if result.Status != StatusFailed {
		t.Fatalf("状态 = %q，期望 failed", result.Status)
	}
	if got := readVersion(t, f.target); got != "1.0.0" {
		t.Fatalf("替换失败后目标版本 = %q，期望 1.0.0", got)
	}
	if len(relaunched) != 1 {
		t.Fatalf("应重新拉起旧版本一次，实际 %v", relaunched)
	}
}

func TestRunPostSwapValidationFailsRollsBack(t *testing.T) {
	f := newFixture(t)
	var relaunched []string
	hooks := baseHooks(t, &relaunched)
	calls := 0
	hooks.Validate = func(path string) error {
		calls++
		if calls == 1 {
			return nil // 暂存包预校验通过
		}
		return errors.New("签名校验失败")
	}
	result := Run(f.job, hooks)

	if result.Status != StatusRolledBack {
		t.Fatalf("状态 = %q，期望 rolled_back；error=%s", result.Status, result.Error)
	}
	if got := readVersion(t, f.target); got != "1.0.0" {
		t.Fatalf("回滚后目标版本 = %q，期望 1.0.0", got)
	}
	if len(relaunched) != 1 || relaunched[0] != f.target {
		t.Fatalf("应重新拉起旧版本一次，实际 %v", relaunched)
	}
}

func TestRunRelaunchFailureRollsBack(t *testing.T) {
	f := newFixture(t)
	var relaunched []string
	hooks := baseHooks(t, &relaunched)
	first := true
	hooks.Relaunch = func(target string) error {
		relaunched = append(relaunched, target)
		if first {
			first = false
			return errors.New("open -a 失败")
		}
		return nil
	}
	result := Run(f.job, hooks)

	if result.Status != StatusRolledBack {
		t.Fatalf("状态 = %q，期望 rolled_back；error=%s", result.Status, result.Error)
	}
	if got := readVersion(t, f.target); got != "1.0.0" {
		t.Fatalf("回滚后目标版本 = %q，期望 1.0.0", got)
	}
	if len(relaunched) != 2 {
		t.Fatalf("应拉起新版本失败后再拉起旧版本，实际 %v", relaunched)
	}
}

func TestRunMissingHooksFails(t *testing.T) {
	f := newFixture(t)
	result := Run(f.job, Hooks{})
	if result.Status != StatusFailed {
		t.Fatalf("状态 = %q，期望 failed", result.Status)
	}
}

func TestJobValidateRejectsRelativePaths(t *testing.T) {
	job := Job{
		Version:            JobVersion,
		ParentPID:          os.Getpid(),
		Staged:             "relative/stage",
		Target:             "/Applications/VaKaoHub.app",
		Backup:             "/Applications/.VaKaoHub.app.old",
		Log:                "/tmp/self-update.log",
		Result:             "/tmp/last-result.json",
		WaitTimeoutSeconds: 60,
	}
	if err := job.Validate(); err == nil {
		t.Fatal("相对路径应校验失败")
	}
}
