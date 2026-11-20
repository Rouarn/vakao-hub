//go:build darwin

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"vakaohub/launcher/internal/contract"
)

func installLocal(format, filePath, appKey, appName, targetPath, installDir string) (string, error) {
	switch format {
	case contract.FormatDMG:
		return installDMG(filePath, targetPath, installDir)
	case contract.FormatZIP:
		return installZip(filePath, targetPath, installDir)
	default:
		return "", fmt.Errorf("macOS 暂不支持 %s 格式，请上传 dmg 或 zip", format)
	}
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
		return fmt.Errorf("安装目录必须是绝对路径")
	}
	if !allowedInstallDir(dir) {
		return fmt.Errorf("只能选择用户主目录内或 /Applications 下的目录")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("安装目录不存在：%s", dir)
	}
	if err := syscall.Access(dir, 0x2 /* W_OK */); err != nil {
		return fmt.Errorf("安装目录不可写：%s", dir)
	}
	return nil
}

// scanInstallDir 是用户配置的安装目录，参与已安装应用扫描。
var scanInstallDir string

// SetInstallDir 记住配置的安装目录并失效扫描缓存；传空串恢复默认。
func SetInstallDir(dir string) {
	scanInstallDir = ExpandDir(dir)
	InvalidateIndex()
}

func installZip(filePath, targetPath, installDir string) (string, error) {
	tmp, err := os.MkdirTemp("", "vakaohub-unzip-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	appPath, err := extractApp(contract.FormatZIP, filePath, tmp)
	if err != nil {
		return "", err
	}
	return copyToApplications(appPath, targetPath, installDir)
}

func installDMG(filePath, targetPath, installDir string) (string, error) {
	tmp, err := os.MkdirTemp("", "vakaohub-dmg-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	appPath, err := extractApp(contract.FormatDMG, filePath, tmp)
	if err != nil {
		return "", err
	}
	return copyToApplications(appPath, targetPath, installDir)
}

// extractApp 把安装包里的 .app 解到 dest，返回解出的 .app 路径。
// zip 直接解压；dmg 先临时挂载再把 .app 复制出来，挂载点用完即卸载。
func extractApp(format, filePath, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}

	switch format {
	case contract.FormatZIP:
		if output, err := exec.Command("ditto", "-x", "-k", filePath, dest).CombinedOutput(); err != nil {
			return "", fmt.Errorf("解压失败：%v %s", err, strings.TrimSpace(string(output)))
		}
		return findApp(dest)
	case contract.FormatDMG:
		mountPoint, err := os.MkdirTemp("", "vakaohub-dmg-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(mountPoint)

		attach := exec.Command(
			"hdiutil", "attach",
			"-nobrowse", "-readonly", "-quiet",
			"-mountpoint", mountPoint,
			filePath,
		)
		if output, err := attach.CombinedOutput(); err != nil {
			return "", fmt.Errorf("挂载 DMG 失败：%v %s", err, strings.TrimSpace(string(output)))
		}
		defer func() {
			_ = exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
		}()

		appPath, err := findApp(mountPoint)
		if err != nil {
			return "", err
		}
		target := filepath.Join(dest, filepath.Base(appPath))
		if output, err := exec.Command("ditto", appPath, target).CombinedOutput(); err != nil {
			return "", fmt.Errorf("复制应用失败：%v %s", err, strings.TrimSpace(string(output)))
		}
		return target, nil
	default:
		return "", fmt.Errorf("macOS 暂不支持 %s 格式，请上传 dmg 或 zip", format)
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
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("安装包内未找到 .app")
	}
	return found, nil
}

// copyToApplications 把解出的 .app 放进安装目录。
// targetPath 非空表示升级已有安装：保持原位置不搬家，但该位置仍须在允许范围内。
func copyToApplications(appPath, targetPath, installDir string) (string, error) {
	dir := ExpandDir(installDir)
	if dir == "" {
		dir = DefaultInstallDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	target := filepath.Join(dir, filepath.Base(appPath))
	if targetPath != "" {
		clean := filepath.Clean(targetPath)
		if !allowedInstallDir(filepath.Dir(clean)) {
			return "", fmt.Errorf("安装路径不在允许的安装目录内：%s", targetPath)
		}
		target = clean
	}
	if err := os.RemoveAll(target); err != nil {
		return "", err
	}
	if output, err := exec.Command("ditto", appPath, target).CombinedOutput(); err != nil {
		return "", fmt.Errorf("复制应用到 %s 失败：%v %s", filepath.Dir(target), err, strings.TrimSpace(string(output)))
	}

	// 代码签名完整性校验（ad-hoc 签名也会通过；未签名或签名损坏会被拒绝）
	if output, err := exec.Command("codesign", "--verify", "--deep", "--strict", target).CombinedOutput(); err != nil {
		_ = os.RemoveAll(target)
		return "", fmt.Errorf("代码签名校验失败：%v %s", err, strings.TrimSpace(string(output)))
	}
	return target, nil
}

// SelfUpdate 替换当前正在运行的启动器 .app。
//
// 运行中的 bundle 不能就地覆盖（进程还映射着旧文件），因此分三步：
// 解压并校验新包到缓存目录 → 启动脱离进程组的后台脚本 → 脚本等本进程退出后
// 把旧包改名备份、放入新包并重新打开；任一步失败都回滚到旧包。
func SelfUpdate(format, filePath string) error {
	current, _, ok := CurrentBundle()
	if !ok {
		return fmt.Errorf("当前不是从 .app 启动，无法自动更新；请手动下载替换")
	}
	// /Applications 属于 root 时替换必然失败，提前拦下比让脚本静默失败更好。
	if err := syscall.Access(filepath.Dir(current), 0x2 /* W_OK */); err != nil {
		return fmt.Errorf("没有权限替换 %s，请手动下载安装", current)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	stage := filepath.Join(cache, "vakaohub", "updates", "stage")
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	staged, err := extractApp(format, filePath, stage)
	if err != nil {
		return err
	}
	if output, err := exec.Command("codesign", "--verify", "--deep", "--strict", staged).CombinedOutput(); err != nil {
		return fmt.Errorf("代码签名校验失败：%v %s", err, strings.TrimSpace(string(output)))
	}
	return startReplaceScript(staged, current, stage)
}

func startReplaceScript(stagedApp, targetApp, stageDir string) error {
	scriptPath := filepath.Join(stageDir, "self-update.sh")
	if err := os.WriteFile(scriptPath, []byte(selfUpdateScript()), 0o700); err != nil {
		return err
	}

	command := exec.Command(
		"/bin/sh", scriptPath,
		strconv.Itoa(os.Getpid()),
		stagedApp,
		targetApp,
		targetApp+".old",
	)
	// 新会话：启动器马上要退出，脚本必须脱离父进程继续跑完替换。
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动更新脚本失败：%w", err)
	}
	return nil
}

// selfUpdateScript 供后台执行：参数依次是旧进程 PID、新 .app、目标 .app、备份路径。
// 最多等 5 分钟旧进程退出；替换失败时回滚并重新打开旧版本，避免用户失去可用的启动器。
func selfUpdateScript() string {
	return `#!/bin/sh
pid="$1"; staged="$2"; target="$3"; backup="$4"
tries=0
while kill -0 "$pid" 2>/dev/null; do
  sleep 0.5
  tries=$((tries + 1))
  if [ "$tries" -ge 600 ]; then break; fi
done
sleep 0.5
rm -rf "$backup"
if [ -e "$target" ] && ! mv "$target" "$backup"; then
  open "$target" >/dev/null 2>&1
  exit 1
fi
if ! ditto "$staged" "$target"; then
  rm -rf "$target"
  if [ -e "$backup" ]; then mv "$backup" "$target"; fi
  open "$target" >/dev/null 2>&1
  exit 1
fi
xattr -dr com.apple.quarantine "$target" >/dev/null 2>&1
open "$target" >/dev/null 2>&1
rm -rf "$backup" "$staged"
exit 0
`
}

func removeLocal(path string) error {
	clean := filepath.Clean(path)
	if filepath.Ext(clean) != ".app" {
		return fmt.Errorf("拒绝删除非 .app 路径：%s", path)
	}
	// 自定义安装目录会随设置变化，因此不再列举固定根目录，
	// 而是校验父目录在允许范围内，并确认目标确实是完整的应用包。
	if !allowedInstallDir(filepath.Dir(clean)) {
		return fmt.Errorf("拒绝删除用户主目录与 /Applications 之外的路径")
	}
	if _, err := os.Stat(filepath.Join(clean, "Contents", "Info.plist")); err != nil {
		return fmt.Errorf("目标不是有效的应用包：%s", path)
	}
	return os.RemoveAll(clean)
}

func launchLocal(path string) error {
	output, err := exec.Command("open", path).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if strings.Contains(message, "not allowed") || strings.Contains(message, "cannot be opened") {
			return fmt.Errorf("系统阻止了启动（Gatekeeper）：请在 Finder 中右键点击应用选择「打开」，或到 系统设置 → 隐私与安全性 选择「仍要打开」")
		}
		return fmt.Errorf("启动失败：%v %s", err, message)
	}
	return nil
}
