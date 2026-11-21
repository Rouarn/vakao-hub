//go:build darwin

package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
	"vakaohub/launcher/internal/contract"
)

func installLocal(ctx context.Context, opts Options) (Result, error) {
	switch opts.Format {
	case contract.FormatDMG, contract.FormatZIP:
		path, err := installBundle(ctx, opts.Format, opts.FilePath, opts.TargetPath, opts.InstallDir)
		return Result{Path: path, Kind: KindPortable}, err
	default:
		return Result{}, apperr.New(
			apperr.KindUnsupported,
			"install.format",
			"该格式不支持自动安装，请上传 dmg 或 zip",
			nil,
		)
	}
}

// installBundle 先在 target 同级暂存目录解出并校验新 bundle，再原子换入。
// 任何一步失败都只清理暂存目录，旧版本保持原样。
func installBundle(ctx context.Context, format, filePath, targetPath, installDir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", apperr.FromContext(err, "install")
	}

	dir := ExpandDir(installDir)
	if dir == "" {
		dir = DefaultInstallDir()
	}
	if dir == "" {
		return "", apperr.New(apperr.KindInternal, "install.dir", "无法确定应用安装目录", nil)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.dir")
	}
	if !allowedInstallDir(dir) {
		return "", apperr.New(
			apperr.KindPermission,
			"install.dir",
			"只能安装到用户主目录内或 /Applications 下的目录",
			nil,
		)
	}

	targetParent := dir
	target := ""
	if targetPath != "" {
		target = filepath.Clean(targetPath)
		if !allowedInstallDir(filepath.Dir(target)) {
			return "", apperr.New(
				apperr.KindPermission,
				"install.dir",
				fmt.Sprintf("安装路径不在允许的安装目录内：%s", targetPath),
				nil,
			)
		}
		targetParent = filepath.Dir(target)
	}
	if err := os.MkdirAll(targetParent, 0o755); err != nil {
		return "", apperr.FromOS(err, "install.dir")
	}

	stage, err := atomicfs.StageDirFor(filepath.Join(targetParent, ".vakaohub-target"))
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)

	stagedApp, err := ExtractApp(ctx, format, filePath, stage)
	if err != nil {
		return "", err
	}
	if err := validateStagedBundle(ctx, stagedApp); err != nil {
		return "", err
	}

	if target == "" {
		target = filepath.Join(dir, filepath.Base(stagedApp))
	}

	if err := commitStage(stagedApp, target); err != nil {
		return "", err
	}
	return target, nil
}

// validateStagedBundle 在换入前确认 bundle 结构完整且签名有效。
func validateStagedBundle(ctx context.Context, appPath string) error {
	if err := ctx.Err(); err != nil {
		return apperr.FromContext(err, "install.verify")
	}
	infoPath := filepath.Join(appPath, "Contents", "Info.plist")
	if _, err := os.Stat(infoPath); err != nil {
		return apperr.New(apperr.KindCorrupt, "install.verify", "安装包内未找到有效的 .app 结构，请重试", err)
	}

	executable := bundleExecutable(ctx, infoPath)
	if executable != "" {
		executablePath := filepath.Join(appPath, "Contents", "MacOS", executable)
		if _, err := os.Stat(executablePath); err != nil {
			return apperr.New(apperr.KindCorrupt, "install.verify", "安装包内未找到可执行文件，请重试", err)
		}
	} else {
		entries, err := os.ReadDir(filepath.Join(appPath, "Contents", "MacOS"))
		if err != nil || len(entries) == 0 {
			return apperr.New(apperr.KindCorrupt, "install.verify", "安装包内未找到可执行文件，请重试", err)
		}
	}

	output, err := exec.CommandContext(ctx, "codesign", "--verify", "--deep", "--strict", appPath).CombinedOutput()
	if err != nil {
		return commandFailure(ctx, apperr.KindSignature, "install.signature", "", err, output)
	}
	return nil
}

func bundleExecutable(ctx context.Context, infoPath string) string {
	output, err := exec.CommandContext(
		ctx, "plutil", "-extract", "CFBundleExecutable", "raw", "-o", "-", infoPath,
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// DefaultInstallDir 返回默认安装目录：~/Applications。
func DefaultInstallDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Applications")
}

// allowedInstallDir 判断目录能否作为安装位置：用户主目录内部或 /Applications 之下。
// 外置卷（/Volumes）与系统目录都不在主目录内，这里自然被拒绝。
func allowedInstallDir(dir string) bool {
	clean := filepath.Clean(dir)
	if clean == "/Applications" || strings.HasPrefix(clean, "/Applications"+string(os.PathSeparator)) {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if clean == home {
		return false
	}
	return strings.HasPrefix(clean, home+string(os.PathSeparator))
}

// ValidateInstallDir 校验用户选择的安装目录：必须已存在、可写且在允许范围内。
// 空串表示使用默认目录，直接通过。
func ValidateInstallDir(dir string) error {
	dir = ExpandDir(dir)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return apperr.New(apperr.KindInternal, "install.dir", "安装目录必须是绝对路径", nil)
	}
	if !allowedInstallDir(dir) {
		return apperr.New(apperr.KindPermission, "install.dir", "只能选择用户主目录内或 /Applications 下的目录", nil)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return apperr.New(apperr.KindInternal, "install.dir", fmt.Sprintf("安装目录不存在：%s", dir), err)
	}
	if err := syscall.Access(dir, 0x2 /* W_OK */); err != nil {
		return apperr.New(apperr.KindPermission, "install.dir", "", err)
	}
	return nil
}

// scanInstallDir 是用户配置的安装目录，参与已安装应用扫描。
var (
	scanDirMu      sync.RWMutex
	scanInstallDir string
)

// SetInstallDir 记住配置的安装目录并失效扫描缓存；传空串恢复默认。
func SetInstallDir(dir string) {
	scanDirMu.Lock()
	scanInstallDir = ExpandDir(dir)
	scanDirMu.Unlock()
	InvalidateIndex()
}

func configuredInstallDir() string {
	scanDirMu.RLock()
	defer scanDirMu.RUnlock()
	return scanInstallDir
}

// CleanupStale 清理 7 天前的暂存与备份残留。
func CleanupStale() {
	atomicfs.CleanupStale(DefaultInstallDir(), 7*24*time.Hour)
	if dir := configuredInstallDir(); dir != "" {
		atomicfs.CleanupStale(dir, 7*24*time.Hour)
	}
}

// ExtractApp 把安装包里的 .app 解到 dest，返回解出的 .app 路径。
// zip 直接解压；dmg 先临时挂载再把 .app 复制出来，挂载点用完即卸载。
func ExtractApp(ctx context.Context, format, filePath, dest string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", apperr.FromContext(err, "extract")
	}
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", apperr.FromOS(err, "extract")
	}

	switch format {
	case contract.FormatZIP:
		output, err := exec.CommandContext(ctx, "ditto", "-x", "-k", filePath, dest).CombinedOutput()
		if err != nil {
			return "", commandFailure(ctx, apperr.KindCorrupt, "extract.zip", "安装包损坏或解压失败，请重试", err, output)
		}
		return findApp(dest)
	case contract.FormatDMG:
		mountPoint, err := os.MkdirTemp("", "vakaohub-dmg-*")
		if err != nil {
			return "", apperr.FromOS(err, "extract.dmg")
		}
		defer os.RemoveAll(mountPoint)

		attach := exec.CommandContext(
			ctx,
			"hdiutil", "attach",
			"-nobrowse", "-readonly", "-quiet",
			"-mountpoint", mountPoint,
			filePath,
		)
		if output, err := attach.CombinedOutput(); err != nil {
			return "", commandFailure(ctx, apperr.KindCorrupt, "extract.dmg", "挂载安装包失败，请重试", err, output)
		}
		defer func() {
			_ = exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
		}()

		appPath, err := findApp(mountPoint)
		if err != nil {
			return "", err
		}
		target := filepath.Join(dest, filepath.Base(appPath))
		output, err := exec.CommandContext(ctx, "ditto", appPath, target).CombinedOutput()
		if err != nil {
			return "", commandFailure(ctx, apperr.KindInternal, "extract.dmg", "复制应用失败，请检查安装目录后重试", err, output)
		}
		return target, nil
	default:
		return "", apperr.New(
			apperr.KindUnsupported,
			"extract.format",
			"该格式不支持自动安装，请上传 dmg 或 zip",
			nil,
		)
	}
}

func findApp(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if found != "" {
			return filepath.SkipDir
		}
		if entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".app") {
			found = path
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", apperr.FromOS(err, "extract.find")
	}
	if found == "" {
		return "", apperr.New(apperr.KindCorrupt, "extract.find", "安装包内未找到 .app，请重试", nil)
	}
	return found, nil
}

func removeLocal(path, _ string) error {
	clean := filepath.Clean(path)
	if filepath.Ext(clean) != ".app" {
		return apperr.New(apperr.KindInternal, "uninstall", fmt.Sprintf("拒绝删除非 .app 路径：%s", path), nil)
	}
	// 自定义安装目录会随设置变化，因此不再列举固定根目录，
	// 而是校验父目录在允许范围内，并确认目标确实是完整的应用包。
	if !allowedInstallDir(filepath.Dir(clean)) {
		return apperr.New(apperr.KindPermission, "uninstall", "拒绝删除用户主目录与 /Applications 之外的路径", nil)
	}
	if _, err := os.Stat(filepath.Join(clean, "Contents", "Info.plist")); err != nil {
		return apperr.New(apperr.KindCorrupt, "uninstall", fmt.Sprintf("目标不是有效的应用包：%s", path), err)
	}
	if err := os.RemoveAll(clean); err != nil {
		return apperr.FromOS(err, "uninstall")
	}
	return nil
}

func launchLocal(path string) error {
	if _, err := os.Stat(path); err != nil {
		return apperr.New(
			apperr.KindInternal,
			"launch",
			fmt.Sprintf("应用已不在原安装位置（%s），请刷新后重试或重新安装", path),
			err,
		)
	}
	output, err := exec.Command("open", path).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "does not exist") || strings.Contains(message, "No such file") {
			return apperr.New(
				apperr.KindInternal,
				"launch",
				fmt.Sprintf("应用已不在原安装位置（%s），请刷新后重试或重新安装", path),
				fmt.Errorf("%w: %s", err, message),
			)
		}
		if strings.Contains(message, "not allowed") || strings.Contains(message, "cannot be opened") {
			return apperr.New(
				apperr.KindPermission,
				"launch",
				"系统阻止了启动（Gatekeeper）：请在 Finder 中右键点击应用选择「打开」，或到 系统设置 → 隐私与安全性 选择「仍要打开」",
				fmt.Errorf("%w: %s", err, message),
			)
		}
		return apperr.New(apperr.KindInternal, "launch", "启动失败，请稍后重试", fmt.Errorf("%w: %s", err, message))
	}
	return nil
}
