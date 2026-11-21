package service

import (
	"path/filepath"
	"testing"

	"vakaohub/launcher/internal/store"
)

// newTestHost 构造一个不依赖 Wails 运行时的 Host，供单元测试使用。
func newTestHost(t *testing.T, st *store.Store) *Host {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return newHost(nil, st)
}
