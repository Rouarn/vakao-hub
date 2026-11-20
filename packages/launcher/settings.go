package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const defaultBaseURL = "https://dist.woyuxiaji.cn/api/v1"

type Settings struct {
	BaseURL string `json:"baseUrl"`
	// DeviceID 是本机匿名标识，首次启动生成后固定在设置文件里，
	// 只用于事件上报的去重与灰度分桶，不含任何硬件信息。
	DeviceID string `json:"deviceId,omitempty"`
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
