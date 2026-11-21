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
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/atomicfs"
	"vakaohub/launcher/internal/contract"
)

const installerTimeout = 30 * time.Minute

func installLocal(ctx context.Context, format, filePath, appKey, appName, targetPath, installDir string) (string, error) {
	switch format {
	case contract.FormatZIP:
		return installZip(ctx, filePath, appKey, targetPath, installDir)
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
			return installPortableEXE(ctx, filePath, appKey, targetPath, installDir)
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

func installZip(ctx context.Context, filePath, appKey, targetPath, installDir string) (string, error) {
	target := resolveContainer(appKey, targetPath, installDir)
	if target == "" {
		return "", apperr.New(apperr.KindInternal, "install.zip", "无法确定应用安装目录，请检查用户配置目录", nil)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", apperr.FromOS(err, "install.zip")
	}

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
func installPortableEXE(ctx context.Context, filePath, appKey, targetPath, installDir string) (string, error) {
	target := resolveContainer(appKey, targetPath, installDir)
	if target == "" {
		return "", apperr.New(apperr.KindInternal, "install.portable", "无法确定应用安装目录，请检查用户配置目录", nil)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", apperr.FromOS(err, "install.portable")
	}
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
	for _, root := range installRoots() {
		atomicfs.CleanupStale(root, 7*24*time.Hour)
	}
}

func removeLocal(path, appKey string) error {
	container, ok := containerOf(path, appKey)
	if !ok {
		return apperr.New(
			apperr.KindPermission,
			"uninstall",
			fmt.Sprintf("拒绝删除无法确认归属的路径：%s", path),
			nil,
		)
	}
	if isDriveRoot(container) || isSystemProtectedDir(container) {
		return apperr.New(
			apperr.KindPermission,
			"uninstall",
			fmt.Sprintf("拒绝删除受保护的路径：%s", container),
			nil,
		)
	}
	if err := os.RemoveAll(container); err != nil {
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

// DefaultInstallDir 返回托管应用的默认安装根目录：%LOCALAPPDATA%\Programs\VaKaoHub。
// 放在 Local 而非 Roaming，避免可执行文件被漫游配置同步；%LOCALAPPDATA% 缺失时
// 回落到 1.1.2 及更早版本的旧落点，保证始终有确定可写的位置。
func DefaultInstallDir() string {
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		return filepath.Join(base, "Programs", "VaKaoHub")
	}
	return legacyInstallDir()
}

// legacyInstallDir 是旧版本的固定落点，仍参与识别、卸载与残留清理，
// 避免用户手动删掉文件后界面还留着记录，或反过来完全识别不到。
func legacyInstallDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "VaKaoHub", "apps")
}

// installRoot 返回实际使用的安装根目录，空配置回落到默认目录。
func installRoot(installDir string) string {
	if root := ExpandDir(installDir); root != "" {
		return root
	}
	return DefaultInstallDir()
}

// installRoots 汇总需要扫描与清理的根目录：默认、旧版落点、当前配置。
func installRoots() []string {
	seen := make(map[string]struct{}, 3)
	roots := make([]string, 0, 3)
	for _, dir := range []string{DefaultInstallDir(), legacyInstallDir(), configuredInstallDir()} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		key := strings.ToLower(filepath.Clean(dir))
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		roots = append(roots, dir)
	}
	return roots
}

// resolveContainer 决定应用容器目录（<root>\<appKey>）。
// 升级已有安装时必须留在原容器：否则用户改了设置后，一次更新就会把应用静默搬家，
// 旧目录还会残留一份。只有反推不出容器时才回落到当前配置/默认目录。
func resolveContainer(appKey, targetPath, installDir string) string {
	if container, ok := containerOf(targetPath, appKey); ok {
		return container
	}
	root := installRoot(installDir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, appKey)
}

// containerOf 从安装路径向上找到名为 appKey 的那一级目录（大小写不敏感）。
func containerOf(path, appKey string) (string, bool) {
	appKey = strings.TrimSpace(appKey)
	if strings.TrimSpace(path) == "" || appKey == "" {
		return "", false
	}
	clean := filepath.Clean(path)
	for {
		if strings.EqualFold(filepath.Base(clean), appKey) {
			return clean, true
		}
		parent := filepath.Dir(clean)
		if parent == clean {
			return "", false
		}
		clean = parent
	}
}

// ValidateInstallDir 校验用户选择的便携应用目录：
// 必须已存在、可写、位于本机固定磁盘，且不是盘符根或系统目录。
func ValidateInstallDir(dir string) error {
	dir = ExpandDir(dir)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return apperr.New(apperr.KindInternal, "install.dir", "安装目录必须是绝对路径", nil)
	}
	clean := filepath.Clean(dir)
	if isDriveRoot(clean) {
		return apperr.New(apperr.KindInternal, "install.dir", "请不要直接选择磁盘根目录，请新建一个专用目录", nil)
	}
	if err := validateFixedVolume(clean); err != nil {
		return err
	}
	if isProtectedInstallRoot(clean) {
		return apperr.New(apperr.KindPermission, "install.dir", "不能选择系统目录或启动器配置目录，请换一个位置", nil)
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return apperr.New(apperr.KindInternal, "install.dir", fmt.Sprintf("安装目录不存在：%s", clean), err)
	}
	if !IsDirWritable(clean) {
		return apperr.New(apperr.KindPermission, "install.dir", "", nil)
	}
	// 还要拼上 <appKey>\<文件名>，给 MAX_PATH 留出余量。
	if len(clean) > maxInstallRootLength {
		return apperr.New(apperr.KindInternal, "install.dir", "目录路径过长，请换一个更短的路径", nil)
	}
	return nil
}

const maxInstallRootLength = 180

// validateFixedVolume 只接受本机固定磁盘：移动盘拔出、网络盘断线都会让应用直接打不开。
func validateFixedVolume(dir string) error {
	volume := filepath.VolumeName(dir)
	if volume == "" {
		return apperr.New(apperr.KindInternal, "install.dir", "安装目录必须是带盘符的本地路径", nil)
	}
	root, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return apperr.New(apperr.KindInternal, "install.dir", "安装目录无效", err)
	}
	switch windows.GetDriveType(root) {
	case windows.DRIVE_FIXED:
		return nil
	case windows.DRIVE_REMOVABLE:
		return apperr.New(
			apperr.KindUnsupported,
			"install.dir",
			"只能选择本机固定磁盘上的目录；移动磁盘拔出后应用会打不开",
			nil,
		)
	case windows.DRIVE_REMOTE:
		return apperr.New(
			apperr.KindUnsupported,
			"install.dir",
			"不能选择网络位置，请使用本机固定磁盘上的目录",
			nil,
		)
	default:
		return apperr.New(apperr.KindUnsupported, "install.dir", "该位置不是可用的本地磁盘，请换一个目录", nil)
	}
}

func isDriveRoot(dir string) bool {
	volume := filepath.VolumeName(dir)
	if volume == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(dir), filepath.Clean(volume+`\`))
}

// isSystemProtectedDir 用于删除校验：只保护系统目录树，不涉及配置目录，
// 否则旧落点 %AppData%\VaKaoHub\apps 下的应用将无法卸载。
func isSystemProtectedDir(dir string) bool {
	clean := strings.ToLower(filepath.Clean(dir))
	for _, key := range []string{"SystemRoot", "ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		root := os.Getenv(key)
		if root == "" {
			continue
		}
		base := strings.ToLower(filepath.Clean(root))
		if clean == base || strings.HasPrefix(clean, base+`\`) {
			return true
		}
	}
	return false
}

// isProtectedInstallRoot 用于选择校验：系统目录、用户主目录根本身、
// 以及启动器配置目录（含旧版 apps 落点）都不接受作为新的安装根目录。
func isProtectedInstallRoot(dir string) bool {
	if isSystemProtectedDir(dir) {
		return true
	}
	if home, err := os.UserHomeDir(); err == nil && strings.EqualFold(filepath.Clean(dir), filepath.Clean(home)) {
		return true
	}
	if config, err := os.UserConfigDir(); err == nil {
		base := filepath.Clean(filepath.Join(config, "vakaohub"))
		clean := filepath.Clean(dir)
		if strings.EqualFold(clean, base) || strings.HasPrefix(strings.ToLower(clean), strings.ToLower(base)+`\`) {
			return true
		}
	}
	return false
}

// scanInstallDir 是用户配置的安装根目录，参与清理与已安装扫描。
var (
	scanDirMu      sync.RWMutex
	scanInstallDir string
)

// SetInstallDir 记住配置的安装根目录并失效扫描缓存；传空串恢复默认。
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
