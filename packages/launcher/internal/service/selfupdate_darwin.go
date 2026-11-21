//go:build darwin

package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"vakaohub/launcher/internal/install"
)

// selfUpdate 替换当前正在运行的启动器 .app。
//
// 运行中的 bundle 不能就地覆盖（进程还映射着旧文件），因此分三步：
// 解压并校验新包到缓存目录 → 启动脱离进程组的后台脚本 → 脚本等本进程退出后
// 把旧包改名备份、放入新包并重新打开；任一步失败都回滚到旧包。
func (s *InstallService) selfUpdate(format, filePath string) error {
	current, _, ok := install.CurrentBundle()
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
	staged, err := install.ExtractApp(format, filePath, stage)
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
