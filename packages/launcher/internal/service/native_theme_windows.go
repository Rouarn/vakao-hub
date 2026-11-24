//go:build windows

package service

import (
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// SetNativeTheme 同步 Windows 原生标题栏的明暗与配色，使其跟随应用内主题：
// 沉浸式深色负责明暗切换；Win11 上再用标题栏/文字颜色贴合纸墨配色，
// Win10 会忽略后两个属性（DWM 返回错误），但深色模式仍然生效。
func (s *LauncherService) SetNativeTheme(dark bool) {
	window := s.currentWindow()
	if window == nil {
		return
	}
	handle := window.NativeWindow()
	if handle == nil {
		return
	}
	hwnd := uintptr(handle)

	w32.SetTheme(hwnd, dark)
	if dark {
		w32.SetTitleBarColour(hwnd, w32.RGB(0x12, 0x12, 0x10))
		w32.SetTitleTextColour(hwnd, w32.RGB(0xf2, 0xef, 0xe7))
		return
	}
	w32.SetTitleBarColour(hwnd, w32.RGB(0xf3, 0xf1, 0xec))
	w32.SetTitleTextColour(hwnd, w32.RGB(0x17, 0x15, 0x12))
}
