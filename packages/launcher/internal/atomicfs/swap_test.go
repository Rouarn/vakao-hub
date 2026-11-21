package atomicfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入文件失败：%v", err)
	}
}

func TestStageDirForSibling(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Apps", "Demo.app")

	stage, err := StageDirFor(target)
	if err != nil {
		t.Fatalf("StageDirFor 失败：%v", err)
	}
	if filepath.Dir(stage) != filepath.Join(root, "Apps") {
		t.Fatalf("暂存目录不在 target 同级：%s", stage)
	}
	if !strings.HasPrefix(filepath.Base(stage), stagePrefix) {
		t.Fatalf("暂存目录命名不符合约定：%s", stage)
	}
}

func TestSwapReplacesAndRollback(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Demo.app")
	writeFile(t, filepath.Join(target, "version.txt"), "old")

	stage, err := StageDirFor(target)
	if err != nil {
		t.Fatalf("StageDirFor 失败：%v", err)
	}
	writeFile(t, filepath.Join(stage, "version.txt"), "new")

	rollback, err := Swap(stage, target)
	if err != nil {
		t.Fatalf("Swap 失败：%v", err)
	}
	if content := readFile(t, filepath.Join(target, "version.txt")); content != "new" {
		t.Fatalf("换入后内容 = %q，期望 new", content)
	}

	if err := rollback(); err != nil {
		t.Fatalf("rollback 失败：%v", err)
	}
	if content := readFile(t, filepath.Join(target, "version.txt")); content != "old" {
		t.Fatalf("回滚后内容 = %q，期望 old", content)
	}
}

func TestSwapMissingStagedRestoresOldVersion(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Demo.app")
	writeFile(t, filepath.Join(target, "version.txt"), "old")

	_, err := Swap(filepath.Join(root, "missing-stage"), target)
	if err == nil {
		t.Fatal("staged 不存在时应返回错误")
	}
	if content := readFile(t, filepath.Join(target, "version.txt")); content != "old" {
		t.Fatalf("swap 失败后旧版本内容 = %q，期望 old", content)
	}
}

func TestCleanupBackups(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Demo.app")
	writeFile(t, filepath.Join(target, "version.txt"), "old")

	stage, err := StageDirFor(target)
	if err != nil {
		t.Fatalf("StageDirFor 失败：%v", err)
	}
	writeFile(t, filepath.Join(stage, "version.txt"), "new")
	if _, err := Swap(stage, target); err != nil {
		t.Fatalf("Swap 失败：%v", err)
	}

	if err := CleanupBackups(target); err != nil {
		t.Fatalf("CleanupBackups 失败：%v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(root, "."+filepath.Base(target)+oldMarker+"*"))
	if len(matches) != 0 {
		t.Fatalf("备份未清理：%v", matches)
	}
}

func TestCleanupStale(t *testing.T) {
	root := t.TempDir()
	oldStage := filepath.Join(root, stagePrefix+"old")
	recentStage := filepath.Join(root, stagePrefix+"recent")
	if err := os.MkdirAll(oldStage, 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	if err := os.MkdirAll(recentStage, 0o755); err != nil {
		t.Fatalf("创建目录失败：%v", err)
	}
	stale := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(oldStage, stale, stale); err != nil {
		t.Fatalf("设置时间失败：%v", err)
	}

	CleanupStale(root, 7*24*time.Hour)
	if _, err := os.Stat(oldStage); !os.IsNotExist(err) {
		t.Fatalf("过期暂存目录未清理：%v", err)
	}
	if _, err := os.Stat(recentStage); err != nil {
		t.Fatalf("近期暂存目录被误删：%v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败：%v", path, err)
	}
	return string(data)
}
