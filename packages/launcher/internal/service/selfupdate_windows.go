//go:build windows

package service

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"vakaohub/launcher/internal/apperr"
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
func (s *InstallService) selfUpdate(ctx context.Context, format, filePath string) error {
	target, _, ok := install.CurrentBundle()
	if !ok {
		return apperr.New(
			apperr.KindUnsupported,
			"selfupdate",
			"当前不是从安装位置启动，无法自动更新；请手动下载替换",
			nil,
		)
	}
	// 目标目录不可写时替换必然失败，提前拦下比让脚本静默失败更好。
	if !dirWritable(filepath.Dir(target)) {
		return apperr.New(apperr.KindPermission, "selfupdate", "没有权限替换启动器，请手动下载安装", nil)
	}

	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.SelfUpdate)
	defer cancel()

	cache, err := os.UserCacheDir()
	if err != nil {
		return apperr.FromOS(err, "selfupdate.cache")
	}
	stage := filepath.Join(cache, "vakaohub", "updates", "stage")
	if err := os.RemoveAll(stage); err != nil {
		return apperr.FromOS(err, "selfupdate.stage")
	}
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return apperr.FromOS(err, "selfupdate.stage")
	}
	started := false
	defer func() {
		if !started {
			_ = os.RemoveAll(stage)
		}
	}()

	staged, err := stageExecutable(ctx, format, filePath, stage, filepath.Base(target))
	if err != nil {
		return err
	}
	logPath := filepath.Join(filepath.Dir(stage), "self-update.log")
	if err := startWindowsReplaceScript(staged, target, target+".old", logPath); err != nil {
		return err
	}
	started = true
	return nil
}

// stageExecutable 把待替换的 exe 复制/解压到缓存目录。
// 安装流程的下载文件位于临时目录，Install 返回后会被清理，因此必须复制出来。
func stageExecutable(ctx context.Context, format, filePath, stageDir, name string) (string, error) {
	switch format {
	case contract.FormatEXE:
		target := filepath.Join(stageDir, name)
		if err := copyFileContext(ctx, filePath, target); err != nil {
			return "", err
		}
		return target, nil
	case contract.FormatZIP:
		reader, err := zip.OpenReader(filePath)
		if err != nil {
			return "", apperr.New(apperr.KindCorrupt, "selfupdate.zip", "", err)
		}
		defer reader.Close()

		for _, file := range reader.File {
			if err := ctx.Err(); err != nil {
				return "", apperr.FromContext(err, "selfupdate.zip")
			}
			if file.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(file.Name), ".exe") {
				continue
			}
			target := filepath.Join(stageDir, filepath.Base(file.Name))
			if err := extractZipMember(ctx, file, target); err != nil {
				return "", err
			}
			return target, nil
		}
		return "", apperr.New(apperr.KindCorrupt, "selfupdate.zip", "安装包内未找到可执行文件（.exe），无法完成更新", nil)
	default:
		return "", apperr.New(
			apperr.KindUnsupported,
			"selfupdate.format",
			fmt.Sprintf("启动器暂不支持 %s 格式自动更新，请手动下载安装", format),
			nil,
		)
	}
}

func startWindowsReplaceScript(staged, target, backup, logPath string) error {
	scriptPath := filepath.Join(filepath.Dir(staged), "self-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(selfUpdateScriptWindows()), 0o600); err != nil {
		return apperr.FromOS(err, "selfupdate.script")
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
		return apperr.New(apperr.KindInternal, "selfupdate.script", "启动更新程序失败，请手动下载安装", err)
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

func copyFileContext(ctx context.Context, source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return apperr.FromOS(err, "selfupdate.copy")
	}
	defer in.Close()

	out, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return apperr.FromOS(err, "selfupdate.copy")
	}
	defer out.Close()

	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, "selfupdate.copy")
		}
		read, readErr := in.Read(buffer)
		if read > 0 {
			if _, err := out.Write(buffer[:read]); err != nil {
				return apperr.FromOS(err, "selfupdate.copy")
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return apperr.FromOS(readErr, "selfupdate.copy")
		}
	}
}

// extractZipMember 解压单个成员并创建父目录。
func extractZipMember(ctx context.Context, file *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return apperr.FromOS(err, "selfupdate.zip")
	}
	source, err := file.Open()
	if err != nil {
		return apperr.New(apperr.KindCorrupt, "selfupdate.zip", "", err)
	}
	defer source.Close()

	destination, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, file.Mode())
	if err != nil {
		return apperr.FromOS(err, "selfupdate.zip")
	}
	defer destination.Close()

	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, "selfupdate.zip")
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			if _, err := destination.Write(buffer[:read]); err != nil {
				return apperr.FromOS(err, "selfupdate.zip")
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return apperr.FromOS(readErr, "selfupdate.zip")
		}
	}
}
