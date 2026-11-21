//go:build windows

package service

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"vakaohub/launcher/internal/contract"
	"vakaohub/launcher/internal/install"
)

// windowsCreateNoWindow 让自更新脚本在后台运行，不弹出控制台窗口。
const windowsCreateNoWindow = 0x08000000

// selfUpdate 替换当前正在运行的启动器 exe。
//
// Windows 不允许覆盖或改名正在运行的可执行文件，因此与 macOS 同思路：
// 先把新 exe 落到缓存目录（stage），再启动一个脱离父进程的 PowerShell 脚本；
// 脚本等本进程退出后把旧 exe 改名备份、放入新 exe 并重新拉起，任一步失败就回滚。
func (s *InstallService) selfUpdate(format, filePath string) error {
	target, _, ok := install.CurrentBundle()
	if !ok {
		return fmt.Errorf("当前不是从安装位置启动，无法自动更新；请手动下载替换")
	}
	// 目标目录不可写时替换必然失败，提前拦下比让脚本静默失败更好。
	if !dirWritable(filepath.Dir(target)) {
		return fmt.Errorf("没有权限替换 %s，请手动下载安装", target)
	}

	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	stage := filepath.Join(cache, "vakaohub", "updates", "stage")
	if err := os.RemoveAll(stage); err != nil {
		return err
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return err
	}

	staged, err := stageExecutable(format, filePath, stage, filepath.Base(target))
	if err != nil {
		return err
	}
	logPath := filepath.Join(filepath.Dir(stage), "self-update.log")
	return startWindowsReplaceScript(staged, target, target+".old", logPath)
}

// stageExecutable 把待替换的 exe 复制/解压到缓存目录。
// 安装流程的下载文件位于临时目录，Install 返回后会被清理，因此必须复制出来。
func stageExecutable(format, filePath, stageDir, name string) (string, error) {
	switch format {
	case contract.FormatEXE:
		target := filepath.Join(stageDir, name)
		if err := copyFile(filePath, target); err != nil {
			return "", fmt.Errorf("准备更新文件失败：%v", err)
		}
		return target, nil
	case contract.FormatZIP:
		reader, err := zip.OpenReader(filePath)
		if err != nil {
			return "", err
		}
		defer reader.Close()

		for _, file := range reader.File {
			if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(file.Name), ".exe") {
				continue
			}
			target := filepath.Join(stageDir, filepath.Base(file.Name))
			if err := extractZipMember(file, target); err != nil {
				return "", err
			}
			return target, nil
		}
		return "", fmt.Errorf("压缩包内未找到可执行文件（.exe），无法完成更新")
	default:
		return "", fmt.Errorf("Windows 启动器暂不支持 %s 格式自动更新，请手动下载安装", format)
	}
}

func startWindowsReplaceScript(staged, target, backup, logPath string) error {
	scriptPath := filepath.Join(filepath.Dir(staged), "self-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(selfUpdateScriptWindows()), 0o600); err != nil {
		return err
	}

	command := exec.Command(
		"powershell.exe",
		"-NoProfile", "-NonInteractive",
		"-ExecutionPolicy", "Bypass",
		"-WindowStyle", "Hidden",
		"-File", scriptPath,
		"-ProcessId", strconv.Itoa(os.Getpid()),
		"-Staged", staged,
		"-Target", target,
		"-Backup", backup,
		"-LogPath", logPath,
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windowsCreateNoWindow,
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("启动更新脚本失败：%w", err)
	}
	// 脚本要等本进程退出后继续跑，不能 Wait；释放句柄即可。
	_ = command.Process.Release()
	return nil
}

// selfUpdateScriptWindows 供后台执行：参数依次是旧进程 PID、新 exe、目标 exe、备份路径、日志路径。
// 最多等 5 分钟旧进程退出；替换失败时回滚并保持旧版本可用，避免用户失去启动器。
func selfUpdateScriptWindows() string {
	return `param(
  [int]$ProcessId,
  [string]$Staged,
  [string]$Target,
  [string]$Backup,
  [string]$LogPath
)
$ErrorActionPreference = 'SilentlyContinue'
function Write-Log($Message) {
  try { Add-Content -LiteralPath $LogPath -Value ("{0} {1}" -f (Get-Date).ToString('s'), $Message) } catch {}
}
Write-Log "wait for process $ProcessId"
$deadline = (Get-Date).AddMinutes(5)
while ($null -ne (Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)) {
  if ((Get-Date) -gt $deadline) { Write-Log 'timeout waiting for process exit'; break }
  Start-Sleep -Milliseconds 400
}
Start-Sleep -Milliseconds 500

$replaced = $false
for ($i = 0; $i -lt 40; $i++) {
  try {
    if (Test-Path -LiteralPath $Backup) { Remove-Item -LiteralPath $Backup -Force -ErrorAction Stop }
    if (Test-Path -LiteralPath $Target) { Move-Item -LiteralPath $Target -Destination $Backup -Force -ErrorAction Stop }
    Move-Item -LiteralPath $Staged -Destination $Target -Force -ErrorAction Stop
    $replaced = $true
    break
  } catch {
    Write-Log ("replace attempt {0} failed: {1}" -f $i, $_.Exception.Message)
    if (-not (Test-Path -LiteralPath $Target) -and (Test-Path -LiteralPath $Backup)) {
      Move-Item -LiteralPath $Backup -Destination $Target -Force
    }
    Start-Sleep -Milliseconds 250
  }
}

if ($replaced) {
  Write-Log 'replaced'
  if (Test-Path -LiteralPath $Backup) { Remove-Item -LiteralPath $Backup -Force }
  Start-Process -FilePath $Target -WorkingDirectory (Split-Path -Parent $Target)
  Write-Log 'relaunched'
  exit 0
}

Write-Log 'replace failed; keep old version'
if ((-not (Test-Path -LiteralPath $Target)) -and (Test-Path -LiteralPath $Backup)) {
  Move-Item -LiteralPath $Backup -Destination $Target -Force
  Start-Process -FilePath $Target -WorkingDirectory (Split-Path -Parent $Target)
}
exit 1
`
}

// dirWritable 通过创建临时文件确认目录可写。
func dirWritable(dir string) bool {
	probe, err := os.CreateTemp(dir, ".vakaohub-write-*")
	if err != nil {
		return false
	}
	name := probe.Name()
	probe.Close()
	_ = os.Remove(name)
	return true
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// extractZipMember 解压单个成员并创建父目录。
func extractZipMember(file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	source, err := file.Open()
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	if _, err := io.Copy(destination, source); err != nil {
		destination.Close()
		return err
	}
	return destination.Close()
}
