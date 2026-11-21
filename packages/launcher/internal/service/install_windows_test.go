//go:build windows

package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

// TestInstallUncertainEXEAsksThenResumes 覆盖「判不准 -> 二选一 -> 继续安装」全流程：
// 首次调用必须返回 choice 且不写记录；选择便携后从保留的产物续装，不重新下载。
func TestInstallUncertainEXEAsksThenResumes(t *testing.T) {
	root := t.TempDir()

	payload := executableSample(t, true)
	digest := sha256.Sum256(payload)
	downloads := 0

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/catalog":
			body := fmt.Sprintf(`{"code":200,"message":"ok","data":{"platform":"win32","arch":"amd64","apps":[{"id":1,"appKey":"demo","name":"Demo","latest":{"id":1,"versionName":"1.0.0","versionCode":1,"status":1,"minVersionCode":0,"publishedAt":1,"artifacts":[{"id":11,"platform":"win32","arch":"amd64","format":"exe","fileName":"demo.exe","size":%d,"sha256":"%s","downloadUrl":"%s/download"}]}}]}}`,
				len(payload), hex.EncodeToString(digest[:]), "http://"+request.Host)
			_, _ = writer.Write([]byte(body))
		case "/download":
			downloads++
			_, _ = writer.Write(payload)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	installDir := filepath.Join(root, "apps")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("创建安装目录失败：%v", err)
	}

	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: server.URL + "/api/v1", InstallDir: installDir, DeviceID: "test"})
	service := NewInstall(host)

	// 第一次：分类不确定，必须让用户选择，且不能留下「已安装」记录。
	_, err := service.Install(context.Background(), "demo", 11)
	if apperr.KindOf(err) != apperr.KindChoice {
		t.Fatalf("期望 choice 错误，实际：%v", err)
	}
	if _, ok := host.store.Get("demo"); ok {
		t.Fatal("用户选择前不应写入安装记录")
	}

	// 第二次：用户选择便携安装，复用保留的产物，不再下载。
	item, err := service.InstallWithMode(context.Background(), "demo", 11, install.ModePortable)
	if err != nil {
		t.Fatalf("续装失败：%v", err)
	}
	want := filepath.Join(installDir, "demo", "demo.exe")
	if item.InstallPath != want {
		t.Fatalf("安装路径 = %s，期望 %s", item.InstallPath, want)
	}
	if item.Kind != string(install.KindPortable) {
		t.Fatalf("记录类型 = %q，期望 portable", item.Kind)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("安装文件不存在：%v", err)
	}
	if downloads != 1 {
		t.Fatalf("下载次数 = %d，期望 1（选择后应复用保留产物）", downloads)
	}

	// 便携类卸载：删除容器目录并清记录。
	if err := service.Uninstall("demo", false); err != nil {
		t.Fatalf("卸载失败：%v", err)
	}
	if _, err := os.Stat(filepath.Dir(want)); !os.IsNotExist(err) {
		t.Fatalf("卸载后容器目录应消失：%v", err)
	}
	if _, ok := host.store.Get("demo"); ok {
		t.Fatal("卸载后记录应删除")
	}
}

// TestInstallPortableEXERecordsKindAndPath 普通便携 exe 不需要二选一，直接落到配置目录。
func TestInstallPortableEXERecordsKindAndPath(t *testing.T) {
	payload := executableSample(t, false)
	digest := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/catalog":
			body := fmt.Sprintf(`{"code":200,"message":"ok","data":{"platform":"win32","arch":"amd64","apps":[{"id":1,"appKey":"demo","name":"Demo","latest":{"id":1,"versionName":"1.0.0","versionCode":1,"status":1,"minVersionCode":0,"publishedAt":1,"artifacts":[{"id":11,"platform":"win32","arch":"amd64","format":"exe","fileName":"demo.exe","size":%d,"sha256":"%s","downloadUrl":"%s/download"}]}}]}}`,
				len(payload), hex.EncodeToString(digest[:]), "http://"+request.Host)
			_, _ = writer.Write([]byte(body))
		case "/download":
			_, _ = bytes.NewReader(payload).WriteTo(writer)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// 不设置 InstallDir，验证默认落点 %LOCALAPPDATA%\Programs\VaKaoHub。
	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: server.URL + "/api/v1", DeviceID: "test"})
	item, err := NewInstall(host).Install(context.Background(), "demo", 11)
	if err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	want := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "VaKaoHub", "demo", "demo.exe")
	if item.InstallPath != want {
		t.Fatalf("默认落点 = %s，期望 %s", item.InstallPath, want)
	}
}

// TestE2ESettingsCustomDirOnAnotherDrive 走真实设置路径：保存到另一个固定盘、
// 落盘后仍能读回，随后新安装的应用落到该盘（同时验证跨盘下载 -> 暂存 -> 换入）。
func TestE2ESettingsCustomDirOnAnotherDrive(t *testing.T) {
	if os.Getenv("VAKAO_HUB_E2E") != "1" {
		t.Skip("set VAKAO_HUB_E2E=1 to run")
	}
	if _, err := os.Stat(`D:\`); err != nil {
		t.Skipf("本机没有 D 盘：%v", err)
	}

	custom := filepath.Join(`D:\Temp\vakaohub-verify`, "custom-apps")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("创建 D 盘目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(custom) })

	payload := executableSample(t, false)
	archive := zipSample(t, "app.exe", payload)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/catalog":
			body := fmt.Sprintf(`{"code":200,"message":"ok","data":{"platform":"win32","arch":"amd64","apps":[{"id":1,"appKey":"demo","name":"Demo","latest":{"id":1,"versionName":"1.0.0","versionCode":1,"status":1,"minVersionCode":0,"publishedAt":1,"artifacts":[{"id":11,"platform":"win32","arch":"amd64","format":"zip","fileName":"demo.zip","size":0,"sha256":"%s","downloadUrl":"%s/download"}]}}]}}`,
				hex.EncodeToString(digest[:]), "http://"+request.Host)
			_, _ = writer.Write([]byte(body))
		case "/download":
			_, _ = writer.Write(archive)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	host := newTestHost(t, store.NewMemory())
	settingsService := NewSettings(host)
	if _, err := settingsService.SaveSettings(server.URL+"/api/v1", custom); err != nil {
		t.Fatalf("保存 D 盘目录失败：%v", err)
	}
	if persisted := loadSettings(); persisted.InstallDir != custom {
		t.Fatalf("落盘目录 = %q，期望 %q", persisted.InstallDir, custom)
	}

	item, err := NewInstall(host).Install(context.Background(), "demo", 11)
	if err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	want := filepath.Join(custom, "demo", "app.exe")
	if item.InstallPath != want {
		t.Fatalf("安装路径 = %s，期望 %s", item.InstallPath, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("D 盘安装文件不存在：%v", err)
	}
}

func zipSample(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	entry, err := writer.Create(name)
	if err != nil {
		t.Fatalf("写入 zip 条目失败：%v", err)
	}
	if _, err := entry.Write(data); err != nil {
		t.Fatalf("写入 zip 内容失败：%v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭 zip 失败：%v", err)
	}
	return buffer.Bytes()
}

// executableSample 用当前测试二进制构造合法的 PE 样本：
// 抹掉分类器自己的标记字符串，避免测试样本被自身的字符串字面量干扰。
// stripPortable 为 true 时同时抹掉便携框架标记，得到「不确定」样本。
func executableSample(t *testing.T, stripPortable bool) []byte {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("取不到测试可执行文件：%v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("读取测试可执行文件失败：%v", err)
	}
	markers := []string{
		"NullsoftInst", "Inno Setup", "InnoSetupLdr", "WixBundle", "InstallShield",
		"Squirrel", ";!@Install@!UTF-8!", "7zS.sfx", "WinRAR SFX", "Advanced Installer",
		"Setup Factory", "InstallAware", "InstallAnywhere", ".ndata", ".wixburn",
	}
	if stripPortable {
		markers = append(markers,
			"MEIPASS", "PYINSTALLER", "app.asar", "__TAURI", "tauri://",
			"Go build ID", ".go.buildinfo", ".netcorebundle",
		)
	}
	for _, marker := range markers {
		needle := []byte(marker)
		for {
			index := bytes.Index(data, needle)
			if index < 0 {
				break
			}
			for offset := 0; offset < len(needle); offset++ {
				data[index+offset] = 0
			}
		}
	}
	return data
}
