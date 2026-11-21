//go:build !darwin && !windows

package install

import (
	"archive/zip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
	"vakaohub/launcher/internal/contract"
)

func installLocal(ctx context.Context, format, filePath, appKey, appName, targetPath, installDir string) (string, error) {
	switch format {
	case contract.FormatZIP:
		return installZip(ctx, filePath, appKey)
	case contract.FormatAppImage:
		return installAppImage(ctx, filePath, installDir)
	default:
		return "", apperr.New(
			apperr.KindUnsupported,
			"install.format",
			fmt.Sprintf("Linux 暂不支持 %s 格式，请上传 zip 或 appimage", format),
			nil,
		)
	}
}

func installZip(ctx context.Context, filePath, appKey string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}
	root := filepath.Join(home, ".local", "share", "vakaohub", "apps")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}
	target := filepath.Join(root, appKey)

	stage, err := atomicfs.StageDirFor(target)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return "", apperr.New(apperr.KindCorrupt, "install.zip", "", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			return "", apperr.FromContext(err, "install.zip")
		}
		name := filepath.Join(stage, file.Name)
		if !strings.HasPrefix(name, stage+string(os.PathSeparator)) {
			return "", apperr.New(apperr.KindCorrupt, "install.zip", "安装包包含非法路径，请重试", nil)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(name, 0o755); err != nil {
				return "", apperr.FromOS(err, "install.zip")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", apperr.FromOS(err, "install.zip")
		}
		source, err := file.Open()
		if err != nil {
			return "", apperr.New(apperr.KindCorrupt, "install.zip", "", err)
		}
		destination, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			source.Close()
			return "", apperr.FromOS(err, "install.zip")
		}
		copyErr := copyWithContext(ctx, destination, source)
		source.Close()
		destination.Close()
		if copyErr != nil {
			return "", apperr.FromContext(copyErr, "install.zip")
		}
	}

	rollback, err := swapDirectory(stage, target)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = rollback()
		}
	}()
	if _, err := os.Stat(target); err != nil {
		return "", apperr.FromOS(err, "install.swap")
	}
	committed = true
	if err := atomicfs.CleanupBackups(target); err != nil {
		slog.Warn("清理旧版本备份失败", "target", target, "error", err)
	}
	return target, nil
}

func installAppImage(ctx context.Context, filePath, installDir string) (string, error) {
	root := installRoot(installDir)
	if root == "" {
		return "", apperr.New(apperr.KindInternal, "install.dir", "无法确定应用安装目录", nil)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.appimage")
	}
	target := filepath.Join(root, filepath.Base(filePath))

	stage, err := atomicfs.StageDirFor(target)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	staged := filepath.Join(stage, filepath.Base(filePath))
	if err := copyFileContext(ctx, filePath, staged); err != nil {
		return "", err
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.appimage")
	}

	rollback, err := swapDirectory(staged, target)
	if err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = rollback()
		}
	}()
	if _, err := os.Stat(target); err != nil {
		return "", apperr.FromOS(err, "install.swap")
	}
	committed = true
	if err := atomicfs.CleanupBackups(target); err != nil {
		slog.Warn("清理旧版本备份失败", "target", target, "error", err)
	}
	return target, nil
}

func copyFileContext(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return apperr.FromOS(err, "install.copy")
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return apperr.FromOS(err, "install.copy")
	}
	defer out.Close()
	if err := copyWithContext(ctx, out, in); err != nil {
		return apperr.FromContext(err, "install.copy")
	}
	return nil
}

// DefaultInstallDir 返回默认安装目录：~/Applications。
func DefaultInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Applications")
}

// installRoot 返回实际使用的安装根目录，空值回落到默认目录。
func installRoot(dir string) string {
	if expanded := ExpandDir(dir); expanded != "" {
		return expanded
	}
	return DefaultInstallDir()
}

// ValidateInstallDir 校验目录存在、可写且位于用户主目录内。
func ValidateInstallDir(dir string) error {
	dir = ExpandDir(dir)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return apperr.New(apperr.KindInternal, "install.dir", "安装目录必须是绝对路径", nil)
	}
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(dir, home+string(os.PathSeparator)) {
		return apperr.New(apperr.KindPermission, "install.dir", "只能选择用户主目录内的目录", nil)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return apperr.New(apperr.KindInternal, "install.dir", fmt.Sprintf("安装目录不存在：%s", dir), err)
	}
	probe, err := os.CreateTemp(dir, ".vakaohub-write-*")
	if err != nil {
		return apperr.New(apperr.KindPermission, "install.dir", "", err)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

// SetInstallDir Linux 的扫描只覆盖 ~/Applications，配置目录仅用于安装落点。
func SetInstallDir(dir string) {}

// CleanupStale 清理 7 天前的暂存与备份残留。
func CleanupStale() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	atomicfs.CleanupStale(filepath.Join(home, ".local", "share", "vakaohub", "apps"), 7*24*time.Hour)
	atomicfs.CleanupStale(DefaultInstallDir(), 7*24*time.Hour)
}

func removeLocal(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return apperr.FromOS(err, "uninstall")
	}
	clean := filepath.Clean(path)
	if clean == home || !strings.HasPrefix(clean, home+string(os.PathSeparator)) {
		return apperr.New(apperr.KindPermission, "uninstall", "拒绝删除用户主目录之外的路径", nil)
	}
	if err := os.RemoveAll(clean); err != nil {
		return apperr.FromOS(err, "uninstall")
	}
	return nil
}

func launchLocal(path string) error {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		command := exec.Command(path)
		if err := command.Start(); err != nil {
			return apperr.New(apperr.KindInternal, "launch", "启动失败，请稍后重试", err)
		}
		return nil
	}
	output, err := exec.Command("xdg-open", path).CombinedOutput()
	if err != nil {
		return apperr.New(
			apperr.KindInternal,
			"launch",
			"启动失败，请稍后重试",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))),
		)
	}
	return nil
}
