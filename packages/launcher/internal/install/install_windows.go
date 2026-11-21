//go:build windows

package install

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
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
		// 单文件便携应用没有安装器可调起，复制进托管目录并记录安装结果；
		// 安装器（NSIS/Inno 等）仍按原来的方式交给系统向导。
		if !isInstallerEXE(filePath) {
			return installPortableEXE(ctx, filePath, appKey)
		}
		command := exec.Command("cmd", "/c", "start", "", filePath)
		// 安装器/便携程序通常按自身目录查找资源，不能继承启动器的工作目录。
		command.Dir = filepath.Dir(filePath)
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
	root := filepath.Join(base, "VaKaoHub", "apps")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}
	target := filepath.Join(root, appKey)

	stage, err := atomicfs.StageDirFor(target)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	if err := extractZipTo(ctx, filePath, stage); err != nil {
		return "", err
	}

	stageExe := findExecutable(stage)
	if stageExe == "" {
		return "", apperr.New(apperr.KindCorrupt, "install.zip", "安装包内未找到可执行文件（.exe），无法完成安装", nil)
	}
	relative, err := filepath.Rel(stage, stageExe)
	if err != nil {
		return "", apperr.New(apperr.KindInternal, "install.zip", "安装包路径异常，请重试", err)
	}

	if err := commitStage(stage, target); err != nil {
		return "", err
	}
	return filepath.Join(target, relative), nil
}

// installerMarkers 常见 Windows 安装器写在文件里的签名。
// NSIS（electron-builder / Tauri 的默认安装器）含 NullsoftInst。
var installerMarkers = [][]byte{
	[]byte("NullsoftInst"),
	[]byte("Inno Setup"),
	[]byte("WixBundle"),
	[]byte("InstallShield"),
	[]byte("Squirrel"),
}

// isInstallerEXE 判断 exe 是安装向导还是单文件便携应用。
// 读不到文件时按安装器处理，让后续 Start 返回可诊断的错误而不是误复制。
func isInstallerEXE(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer file.Close()

	const window = 4 << 20
	buffer := make([]byte, window)
	n, err := io.ReadFull(file, buffer)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return true
	}
	buffer = buffer[:n]
	for _, marker := range installerMarkers {
		if bytes.Contains(buffer, marker) {
			return true
		}
	}
	// 部分安装器把签名放在文件尾部 overlay，补查一次尾部。
	if info, statErr := file.Stat(); statErr == nil && info.Size() > window {
		tail := make([]byte, window)
		if _, readErr := file.ReadAt(tail, info.Size()-int64(window)); readErr == nil {
			for _, marker := range installerMarkers {
				if bytes.Contains(tail, marker) {
					return true
				}
			}
		}
	}
	return false
}

// installPortableEXE 把没有安装器的单文件应用复制进托管目录，
// 返回可直接记录的启动路径；暂存与换入复用 zip 安装的原子流程。
func installPortableEXE(ctx context.Context, filePath, appKey string) (string, error) {
	root := DefaultInstallDir()
	if strings.TrimSpace(root) == "" {
		return "", apperr.New(apperr.KindInternal, "install.portable", "无法确定应用安装目录，请检查用户配置目录", nil)
	}
	target := filepath.Join(root, appKey)
	stage, err := atomicfs.StageDirFor(target)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	name := filepath.Base(filePath)
	source, err := os.Open(filePath)
	if err != nil {
		return "", apperr.FromOS(err, "install.portable")
	}
	defer source.Close()

	destination, err := os.OpenFile(filepath.Join(stage, name), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", apperr.FromOS(err, "install.portable")
	}
	if err := copyWithContext(ctx, destination, source); err != nil {
		_ = destination.Close()
		return "", apperr.FromContext(err, "install.portable")
	}
	if err := destination.Close(); err != nil {
		return "", apperr.FromOS(err, "install.portable")
	}
	if err := commitStage(stage, target); err != nil {
		return "", err
	}
	return filepath.Join(target, name), nil
}

// findExecutable 选择层级最浅的 exe：WireDeck 这类包顶层是主程序，
// 子目录里还带着 adb/scrcpy 等工具，不能按字典序误选子目录里的程序。
func findExecutable(root string) string {
	found := ""
	foundDepth := -1
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".exe") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := strings.Count(relative, string(os.PathSeparator))
		if found == "" || depth < foundDepth {
			found = path
			foundDepth = depth
		}
		return nil
	})
	return found
}

// CleanupStale 清理 7 天前的暂存与备份残留。
func CleanupStale() {
	atomicfs.CleanupStale(DefaultInstallDir(), 7*24*time.Hour)
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
	command := exec.Command("cmd", "/c", "start", "", path)
	// 便携应用的资源相对自身目录解析；启动器的工作目录可能是 System32。
	command.Dir = filepath.Dir(path)
	output, err := command.CombinedOutput()
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
