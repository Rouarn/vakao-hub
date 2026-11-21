package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"vakaohub/launcher/internal/store"
)

// 并发保存/读取设置不得触发 data race（配合 go test -race）。
func TestSettingsConcurrentAccess(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	settings := NewSettings(host)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				url := fmt.Sprintf("https://example.com/%d/%d", worker, i)
				if _, err := settings.SaveSettings(url, ""); err != nil {
					t.Errorf("SaveSettings: %v", err)
					return
				}
				if got := settings.GetSettings(); got.BaseURL == "" {
					t.Errorf("GetSettings 返回空地址")
					return
				}
				if got := host.settings.BaseURL(); got == "" {
					t.Errorf("BaseURL 返回空地址")
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	// 路径必须和实现走同一套推导，不能按 macOS 目录布局写死，否则 Linux runner 上必然找不到文件。
	dir, err := configDir()
	if err != nil {
		t.Fatalf("解析配置目录: %v", err)
	}
	path := filepath.Join(dir, "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取设置文件: %v", err)
	}
	var persisted Settings
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析设置文件: %v", err)
	}
	if persisted.BaseURL == "" {
		t.Fatalf("设置文件中的地址为空")
	}
}

// 配置目录不可用时必须降级为内存 store，不能让 host.store 为 nil。
func TestNewHostFallsBackToMemoryStore(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", blocker)

	host := NewHost(nil)
	if host.store == nil {
		t.Fatal("store 为 nil")
	}
	item := store.Installed{AppKey: "wiredeck", Name: "WireDeck", InstallPath: "/tmp/wiredeck"}
	if err := host.store.Put(item); err != nil {
		t.Fatalf("内存 store Put: %v", err)
	}
	if _, ok := host.store.Get("wiredeck"); !ok {
		t.Fatal("内存 store 未保存记录")
	}
}
