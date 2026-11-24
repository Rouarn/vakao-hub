package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type Installed struct {
	AppKey      string `json:"appKey"`
	Name        string `json:"name"`
	VersionName string `json:"versionName"`
	VersionCode int    `json:"versionCode"`
	InstallPath string `json:"installPath"`
	InstalledAt int64  `json:"installedAt"`
	// Kind 区分托管复制（portable）与自带安装器（installer）。
	// 旧记录没有该字段时按 portable 处理。
	Kind string `json:"kind,omitempty"`
	// Pending 表示安装器已启动、位置还没从卸载注册表确认。
	Pending bool `json:"pending,omitempty"`
}

type Store struct {
	path string
	// mu 保护 apps 与落盘顺序：多个应用可以同时安装，Put/Remove 会并发进来。
	mu   sync.Mutex
	apps map[string]Installed
}

func NewMemory() *Store {
	return &Store{apps: map[string]Installed{}}
}

func Open() (*Store, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return NewMemory(), nil
	}
	base := filepath.Join(dir, "vakaohub")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		path: filepath.Join(base, "installed.json"),
		apps: map[string]Installed{},
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var items []Installed
	if err := json.Unmarshal(data, &items); err != nil {
		return s, nil
	}
	for _, item := range items {
		s.apps[item.AppKey] = item
	}
	return s, nil
}

func (s *Store) Get(appKey string) (Installed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.apps[appKey]
	return item, ok
}

func (s *Store) List() []Installed {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

func (s *Store) listLocked() []Installed {
	items := make([]Installed, 0, len(s.apps))
	for _, item := range s.apps {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (s *Store) Put(item Installed) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apps[item.AppKey] = item
	return s.saveLocked()
}

func (s *Store) Remove(appKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.apps, appKey)
	return s.saveLocked()
}

// saveLocked 要求调用方已持有 s.mu；写入走同一把锁，避免并发安装抢同一个 .tmp 文件。
func (s *Store) saveLocked() error {
	if s.path == "" {
		// 内存模式：目录不可用时仍保持本次会话可用，不落盘
		return nil
	}
	items := s.listLocked()
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
