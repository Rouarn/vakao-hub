//go:build windows

package install

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32                        = windows.NewLazySystemDLL("shell32.dll")
	ole32                          = windows.NewLazySystemDLL("ole32.dll")
	procSHParseDisplayName         = shell32.NewProc("SHParseDisplayName")
	procSHOpenFolderAndSelectItems = shell32.NewProc("SHOpenFolderAndSelectItems")
	procCoInitializeEx             = ole32.NewProc("CoInitializeEx")
	procCoUninitialize             = ole32.NewProc("CoUninitialize")
	procCoTaskMemFree              = ole32.NewProc("CoTaskMemFree")
)

// Reveal 在资源管理器中选中指定文件或目录。
// 直接调用 Shell API，由系统处理反斜杠、空格与长路径，不经过命令行拼接。
func Reveal(path string) error {
	target, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("路径无效: %w", err)
	}

	// COINIT_APARTMENTTHREADED；已初始化时返回 RPC_E_CHANGED_MODE，不影响调用。
	_, _, _ = procCoInitializeEx.Call(0, 2)
	defer procCoUninitialize.Call()

	var item uintptr
	hresult, _, _ := procSHParseDisplayName.Call(
		uintptr(unsafe.Pointer(target)),
		0,
		uintptr(unsafe.Pointer(&item)),
		0,
		0,
	)
	if hresult != 0 {
		return fmt.Errorf("无法解析路径 %s（HRESULT 0x%x）", path, hresult)
	}
	defer procCoTaskMemFree.Call(item)

	hresult, _, _ = procSHOpenFolderAndSelectItems.Call(item, 0, 0, 0)
	if hresult != 0 {
		return fmt.Errorf("无法在资源管理器中定位 %s（HRESULT 0x%x）", path, hresult)
	}
	return nil
}
