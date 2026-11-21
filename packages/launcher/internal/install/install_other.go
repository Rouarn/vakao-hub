//go:build !darwin && !windows

package install

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"vakaohub/launcher/internal/contract"
)

func installLocal(format, filePath, appKey, appName, targetPath, installDir string) (string, error) {
	switch format {
	case contract.FormatZIP:
		return installZip(filePath, appKey)
	case contract.FormatAppImage:
		target := filepath.Join(installRoot(installDir), filepath.Base(filePath))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := copyFile(filePath, target); err != nil {
			return "", err
		}
		if err := os.Chmod(target, 0o755); err != nil {
			return "", err
		}
		return target, nil
	default:
		return "", fmt.Errorf("Linux 暂不支持 %s 格式，请上传 zip 或 appimage", format)
	}
}

func installZip(filePath, appKey string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	target := filepath.Join(home, ".local", "share", "vakaohub", "apps", appKey)
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", err
	}

	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()

	for _, file := range reader.File {
		name := filepath.Join(target, file.Name)
		if !strings.HasPrefix(name, target+string(os.PathSeparator)) {
			return "", fmt.Errorf("压缩包包含非法路径")
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(name, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return "", err
		}
		source, err := file.Open()
		if err != nil {
			return "", err
		}
		destination, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
		if err != nil {
			source.Close()
			return "", err
		}
		if _, err := io.Copy(destination, source); err != nil {
			source.Close()
			destination.Close()
			return "", err
		}
		source.Close()
		destination.Close()
	}
	return target, nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
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
		return fmt.Errorf("安装目录必须是绝对路径")
	}
	home, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(dir, home+string(os.PathSeparator)) {
		return fmt.Errorf("只能选择用户主目录内的目录")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("安装目录不存在：%s", dir)
	}
	probe, err := os.CreateTemp(dir, ".vakaohub-write-*")
	if err != nil {
		return fmt.Errorf("安装目录不可写：%s", dir)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return nil
}

// SetInstallDir Linux 的扫描只覆盖 ~/Applications，配置目录仅用于安装落点。
func SetInstallDir(dir string) {}

func removeLocal(path string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	clean := filepath.Clean(path)
	if clean == home || !strings.HasPrefix(clean, home+string(os.PathSeparator)) {
		return fmt.Errorf("拒绝删除用户主目录之外的路径")
	}
	return os.RemoveAll(clean)
}

func launchLocal(path string) error {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		cmd := exec.Command(path)
		return cmd.Start()
	}
	if output, err := exec.Command("xdg-open", path).CombinedOutput(); err != nil {
		return fmt.Errorf("启动失败：%v %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
