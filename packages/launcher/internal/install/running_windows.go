//go:build windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows      = user32.NewProc("EnumWindows")
	procGetWindowPid     = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible  = user32.NewProc("IsWindowVisible")
	procPostMessageW     = user32.NewProc("PostMessageW")
	wmClose              = 0x0010
	processQueryAccess   = uint32(windows.PROCESS_QUERY_LIMITED_INFORMATION)
	processTerminateFlag = uint32(windows.PROCESS_TERMINATE)
)

// listRunning 用 Toolhelp 快照枚举进程，并按完整映像路径与目标目录比对。
func listRunning(target string) ([]RunningProcess, error) {
	base := target
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		base = filepath.Dir(target)
	}
	base = strings.ToLower(strings.TrimSuffix(filepath.Clean(base), `\`))
	prefix := base + `\`
	self := uint32(os.Getpid())

	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	processes := make([]RunningProcess, 0)
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		pid := entry.ProcessID
		if pid == 0 || pid == self {
			continue
		}
		path := processImagePath(pid)
		if path == "" {
			continue
		}
		lower := strings.ToLower(path)
		if lower != base && !strings.HasPrefix(lower, prefix) {
			continue
		}
		processes = append(processes, RunningProcess{
			PID:  int(pid),
			Name: windows.UTF16ToString(entry.ExeFile[:]),
			Path: path,
		})
	}
	return processes, nil
}

func processImagePath(pid uint32) string {
	handle, err := windows.OpenProcess(processQueryAccess, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		buffer = make([]uint16, 32768)
		size = uint32(len(buffer))
		if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
			return ""
		}
	}
	return windows.UTF16ToString(buffer[:size])
}

// requestQuit 向目标进程的可见顶层窗口发送 WM_CLOSE，等效于用户点关闭按钮。
func requestQuit(processes []RunningProcess) error {
	targets := make(map[uint32]struct{}, len(processes))
	for _, process := range processes {
		targets[uint32(process.PID)] = struct{}{}
	}
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		var pid uint32
		procGetWindowPid.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if _, ok := targets[pid]; ok {
			_, _, _ = procPostMessageW.Call(hwnd, uintptr(wmClose), 0, 0)
		}
		return 1
	})
	_, _, _ = procEnumWindows.Call(callback, 0)
	return nil
}

func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(processQueryAccess|windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// 打不开句柄通常是没有权限，不能据此判定已退出。
		return true
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return true
	}
	return status == uint32(windows.WAIT_TIMEOUT)
}

func forceKill(pid int) error {
	handle, err := windows.OpenProcess(processTerminateFlag, false, uint32(pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.TerminateProcess(handle, 1); err != nil {
		return err
	}
	return nil
}
