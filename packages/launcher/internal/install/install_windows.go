//go:build windows

package install

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

const installerTimeout = 30 * time.Minute

func installLocal(ctx context.Context, format, filePath, appKey, appName, targetPath, installDir string) (string, error) {
	switch format {
	case contract.FormatZIP:
		return installZip(ctx, filePath, appKey)
	case contract.FormatMSI:
		if err := ctx.Err(); err != nil {
			return "", apperr.FromContext(err, "install.msi")
		}
		command := exec.Command("msiexec", "/i", filePath, "/qn", "/norestart")
		if err := runSystemInstaller(ctx, command, installerTimeout); err != nil {
			return "", err
		}
		// MSI 的安装位置由安装包决定，只能从卸载注册表反查；
		// 先失效索引再查，确保刚写入的注册表项可见。
		InvalidateIndex()
		if detected := FindApps(appName); len(detected) > 0 {
			return detected[0].Path, nil
		}
		return "", nil
	case contract.FormatEXE:
		if err := ctx.Err(); err != nil {
			return "", apperr.FromContext(err, "install.exe")
		}
		command := exec.Command("cmd", "/c", "start", "", filePath)
		// exe 启动后由系统安装程序接管，不能再取消。
		if err := command.Start(); err != nil {
			return "", apperr.New(apperr.KindInternal, "install.exe", "启动安装程序失败，请手动安装", err)
		}
		return "", nil
	default:
		return "", apperr.New(
			apperr.KindUnsupported,
			"install.format",
			fmt.Sprintf("Windows 暂不支持 %s 格式", format),
			nil,
		)
	}
}

// runSystemInstaller 启动系统安装器；启动成功后忽略用户取消，仅用超时兜底。
func runSystemInstaller(ctx context.Context, command *exec.Cmd, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return apperr.FromContext(err, "install.system")
	}
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		return apperr.New(
			apperr.KindInternal,
			"install.system",
			"启动安装程序失败，请手动安装",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(output.String())),
		)
	}

	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return apperr.New(
				apperr.KindInternal,
				"install.system",
				"安装程序执行失败，请手动安装",
				fmt.Errorf("%w: %s", err, strings.TrimSpace(output.String())),
			)
		}
		return nil
	case <-time.After(timeout):
		// 不杀进程：系统安装事务需要自行结束，这里只把超时告诉用户。
		return apperr.New(
			apperr.KindTimeout,
			"install.system",
			"",
			fmt.Errorf("安装程序超过 %s 未结束", timeout),
		)
	}
}

func installZip(ctx context.Context, filePath, appKey string) (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}
	target := filepath.Join(base, "VaKaoHub", "apps", appKey)
	if err := os.RemoveAll(target); err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}

	reader, err := zip.OpenReader(filePath)
	if err != nil {
		return "", apperr.New(apperr.KindCorrupt, "install.zip", "", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if err := ctx.Err(); err != nil {
			_ = os.RemoveAll(target)
			return "", apperr.FromContext(err, "install.zip")
		}
		name := filepath.Join(target, file.Name)
		if !strings.HasPrefix(name, target+string(os.PathSeparator)) {
			_ = os.RemoveAll(target)
			return "", apperr.New(apperr.KindCorrupt, "install.zip", "安装包包含非法路径，请重试", nil)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(name, 0o755); err != nil {
				return "", apperr.FromOS(err, "install.zip")
			}
			continue
		}
		if err := extractZipMember(ctx, file, name); err != nil {
			return "", err
		}
	}

	var exePath string
	_ = filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".exe") && exePath == "" {
			exePath = path
		}
		return nil
	})
	if exePath == "" {
		// 没有 exe 就无法记录启动路径，记录安装目录会让“已安装”变成只能打开文件夹；
		// 视为安装失败并清理残留，避免留下无法识别的目录。
		_ = os.RemoveAll(target)
		return "", apperr.New(apperr.KindCorrupt, "install.zip", "安装包内未找到可执行文件（.exe），无法完成安装", nil)
	}
	return exePath, nil
}

func removeLocal(path string) error {
	base, err := os.UserConfigDir()
	if err != nil {
		return apperr.FromOS(err, "uninstall")
	}
	root := filepath.Join(base, "VaKaoHub", "apps")
	clean := filepath.Clean(path)
	if !strings.HasPrefix(clean, root+string(os.PathSeparator)) {
		return apperr.New(
			apperr.KindPermission,
			"uninstall",
			fmt.Sprintf("拒绝删除 %s 之外的路径；MSI 安装请使用系统卸载", root),
			nil,
		)
	}
	if err := os.RemoveAll(clean); err != nil {
		return apperr.FromOS(err, "uninstall")
	}
	return nil
}

func launchLocal(path string) error {
	output, err := exec.Command("cmd", "/c", "start", "", path).CombinedOutput()
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

// extractZipMember 解压单个成员并创建父目录；复制过程检查 ctx。
func extractZipMember(ctx context.Context, file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return apperr.FromOS(err, "install.zip")
	}
	source, err := file.Open()
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "install.zip", "", err)
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return apperr.FromOS(err, "install.zip")
	}
	defer destination.Close()
	return copyWithContext(ctx, destination, source)
}

// DefaultInstallDir 返回 MSI/EXE 之外的 zip 应用落点，与 installZip 保持一致。
func DefaultInstallDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "VaKaoHub", "apps")
}

// ValidateInstallDir Windows 的安装位置由安装包决定，不接受自定义目录。
func ValidateInstallDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return apperr.New(apperr.KindUnsupported, "install.dir", "Windows 的安装位置由安装包决定，无法自定义", nil)
}

// SetInstallDir Windows 不参与目录扫描（MSI 走卸载注册表），保持空实现。
func SetInstallDir(dir string) {}
