package service

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"vakaohub/launcher/internal/install"
)

const defaultBaseURL = "https://dist.woyuxiaji.cn/api/v1"

type Settings struct {
	BaseURL string `json:"baseUrl"`
	// DeviceID 是本机匿名标识，首次启动生成后固定在设置文件里，
	// 只用于事件上报的去重与灰度分桶，不含任何硬件信息。
	DeviceID string `json:"deviceId,omitempty"`
	// InstallDir 是用户自定义的应用安装目录，空值表示平台默认目录
	//（macOS 为 ~/Applications）。已安装应用不会因修改它而迁移。
	InstallDir string `json:"installDir,omitempty"`
}

// SettingsState 提供并发安全的设置读写。
type SettingsState struct {
	mu sync.RWMutex
	v  Settings
}

func NewSettingsState(v Settings) *SettingsState {
	return &SettingsState{v: v}
}

func (s *SettingsState) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v
}

func (s *SettingsState) Set(v Settings) {
	s.mu.Lock()
	s.v = v
	s.mu.Unlock()
}

func (s *SettingsState) BaseURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v.BaseURL
}

func (s *SettingsState) DeviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.v.DeviceID
}

func (s *SettingsState) update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.v
	if err := fn(&next); err != nil {
		return s.v, err
	}
	if err := saveSettings(next); err != nil {
		return s.v, err
	}
	s.v = next
	return next, nil
}

type SettingsService struct {
	host *Host
}

func NewSettings(host *Host) *SettingsService {
	return &SettingsService{host: host}
}

func (s *SettingsService) GetSettings() Settings {
	return s.host.settings.Get()
}

func (s *SettingsService) SaveSettings(baseURL, installDir string) (Settings, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return s.host.settings.Get(), fmt.Errorf("服务地址不能为空")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return s.host.settings.Get(), fmt.Errorf("服务地址必须以 http:// 或 https:// 开头")
	}
	installDir = strings.TrimSpace(installDir)
	if err := install.ValidateInstallDir(installDir); err != nil {
		return s.host.settings.Get(), err
	}

	next, err := s.host.settings.update(func(settings *Settings) error {
		settings.BaseURL = baseURL
		settings.InstallDir = installDir
		return nil
	})
	if err != nil {
		return s.host.settings.Get(), err
	}
	// 目录变化后已安装扫描范围也要跟着变，否则新装的应用刷新后不可见。
	install.SetInstallDir(installDir)
	return next, nil
}

// GetDefaultBaseURL 返回内置的默认服务地址，前端「恢复默认」不再自带副本。
func (s *SettingsService) GetDefaultBaseURL() string {
	return defaultBaseURL
}

// newDeviceID 生成 16 字节随机数的十六进制串；读取系统随机源失败时返回空串，
// 此时上报会省略 deviceId（服务端可选字段），不影响主流程。
func newDeviceID() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return ""
	}
	return "hub-" + hex.EncodeToString(buffer)
}

func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vakaohub"), nil
}

func loadSettings() Settings {
	settings := Settings{BaseURL: defaultBaseURL}
	dir, err := configDir()
	if err != nil {
		return settings
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{BaseURL: defaultBaseURL}
	}
	settings.BaseURL = strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if settings.BaseURL == "" {
		settings.BaseURL = defaultBaseURL
	}
	// 目录不存在或不可写时回落到默认目录，避免带着坏配置启动后处处装不上。
	settings.InstallDir = strings.TrimSpace(settings.InstallDir)
	if err := install.ValidateInstallDir(settings.InstallDir); err != nil {
		settings.InstallDir = ""
	}
	return settings
}

func saveSettings(settings Settings) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "settings.json"))
}
