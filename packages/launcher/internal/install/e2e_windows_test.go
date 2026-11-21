//go:build windows

package install

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"

	"vakaohub/launcher/internal/contract"
)

// 真机验收用例默认跳过：需要联网下载真实产物、会在 %LOCALAPPDATA% 下写入并清理。
// 运行方式：VAKAO_HUB_E2E=1 go test -run TestE2E -v ./internal/install
func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("VAKAO_HUB_E2E") != "1" {
		t.Skip("set VAKAO_HUB_E2E=1 to run")
	}
}

type realArtifact struct {
	key      string
	url      string
	sha256   string
	format   string
	mode     string
	filename string
}

// TestE2ERealPortableArtifacts 真实产物装到默认目录、能启动、能卸载。
func TestE2ERealPortableArtifacts(t *testing.T) {
	requireE2E(t)
	cases := []realArtifact{
		{
			key:      "wiredeck",
			url:      "https://dist.woyuxiaji.cn/api/v1/updates/download/52",
			sha256:   "a8155876ca0c3615c32e4112751c6e872db327ee777a26d1dd316c56ba14f918",
			format:   contract.FormatZIP,
			mode:     ModeAuto,
			filename: "WireDeck-windows-amd64.zip",
		},
		{
			// OnmyojiTools 的 exe 既没有安装器标记也没有可识别的便携框架标记，
			// 真实流程会弹二选一；这里模拟用户选择「便携安装」。
			key:      "onmyoji-tools",
			url:      "https://dist.woyuxiaji.cn/api/v1/updates/download/39",
			sha256:   "e0c2675b65f3ca23504522065a99bd521d399406b7e3e3f9fa82eac45e11c075",
			format:   contract.FormatEXE,
			mode:     ModePortable,
			filename: "OnmyojiTools-3.0.0-win32-x64.exe",
		},
	}

	root := DefaultInstallDir()
	if root == "" {
		t.Fatal("取不到默认安装目录")
	}
	t.Logf("默认安装根目录：%s", root)

	for _, item := range cases {
		t.Run(item.key, func(t *testing.T) {
			archive := downloadVerified(t, item)
			result, err := Install(context.Background(), Options{
				Format:   item.format,
				FilePath: archive,
				AppKey:   item.key,
				AppName:  item.key,
				Mode:     item.mode,
			})
			if err != nil {
				t.Fatalf("安装失败：%v", err)
			}
			wantPrefix := filepath.Join(root, item.key) + string(os.PathSeparator)
			if !strings.HasPrefix(result.Path, wantPrefix) {
				t.Fatalf("安装路径 = %s，期望位于 %s 下", result.Path, wantPrefix)
			}
			info, err := os.Stat(result.Path)
			if err != nil || info.IsDir() {
				t.Fatalf("安装后的可执行文件不可用：%v", err)
			}
			t.Logf("已安装：%s（%d 字节）", result.Path, info.Size())

			// 启动一次，确认不是损坏的复制品；GUI 应用不要求常驻，能拉起即可。
			launchProbe(t, result.Path)

			if err := Remove(result.Path, item.key); err != nil {
				t.Fatalf("卸载失败：%v", err)
			}
			if _, err := os.Stat(filepath.Join(root, item.key)); !os.IsNotExist(err) {
				t.Fatalf("卸载后目录应消失：%v", err)
			}
		})
	}
}

// TestE2ENSISArtifactClassifiedAsInstaller 真实 NSIS 安装包必须走安装器分支。
func TestE2ENSISArtifactClassifiedAsInstaller(t *testing.T) {
	requireE2E(t)
	file := downloadVerified(t, realArtifact{
		key:      "lanlink",
		url:      "https://dist.woyuxiaji.cn/api/v1/updates/download/43",
		sha256:   "05da32d8430f70c038e540f9992c27c0d8c56d00f0f3d47d2e4cc3feb67c3415",
		filename: "LanLink_0.1.8_x64-setup.exe",
	})
	got := Classify(contract.FormatEXE, file)
	if got.Kind != KindInstaller {
		t.Fatalf("LanLink NSIS 应判为安装器，实际 %+v", got)
	}
	t.Logf("LanLink 分类：%+v", got)
}

// TestE2EZipWithSetupRunsInstaller zip 里只有一个 setup.exe 时必须交给安装器，
// 不能把 setup.exe 复制进便携应用目录。这里用系统 whoami.exe 加 NSIS 标记做无害替身。
func TestE2EZipWithSetupRunsInstaller(t *testing.T) {
	requireE2E(t)
	stub, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "whoami.exe"))
	if err != nil {
		t.Skipf("读取 whoami.exe 失败：%v", err)
	}
	// 追加一个安装器签名，让分类器认定它是安装向导。
	payload := append(stub, []byte("\nNullsoftInst\n")...)
	archive := filepath.Join(t.TempDir(), "setup.zip")
	writeZip(t, archive, "setup.exe", payload)

	root := DefaultInstallDir()
	result, err := Install(context.Background(), Options{
		Format: contract.FormatZIP, FilePath: archive, AppKey: "zip-installer-probe", AppName: "Probe",
	})
	if err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	if result.Kind != KindInstaller || !result.Pending {
		t.Fatalf("应走安装器分支，实际 %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, "zip-installer-probe")); !os.IsNotExist(err) {
		t.Fatalf("安装器分支不应在托管目录留下副本：%v", err)
	}
}

// TestE2EUninstallerFromRegistry 验证卸载会读取并使用注册表里的 UninstallString；
// 用一条临时 HKCU 记录探测，不触碰任何真实应用。
func TestE2EUninstallerFromRegistry(t *testing.T) {
	requireE2E(t)
	const probeName = "VaKaoHub E2E Uninstall Probe"
	const keyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\VaKaoHubE2EProbe`

	key, _, err := registry.CreateKey(registry.CURRENT_USER, keyPath, registry.SET_VALUE)
	if err != nil {
		t.Fatalf("创建测试注册表项失败：%v", err)
	}
	if err := key.SetStringValue("DisplayName", probeName); err != nil {
		_ = key.Close()
		t.Fatalf("写入 DisplayName 失败：%v", err)
	}
	_ = key.SetStringValue("UninstallString", `cmd /c exit 0`)
	_ = key.SetStringValue("InstallLocation", t.TempDir())
	_ = key.Close()
	t.Cleanup(func() { _ = registry.DeleteKey(registry.CURRENT_USER, keyPath) })

	command, _, ok := uninstallCommand(probeName)
	if !ok || !strings.Contains(command, "exit 0") {
		t.Fatalf("未读到卸载命令：%q/%v", command, ok)
	}
	InvalidateIndex()
	if err := UninstallInstaller(probeName); err != nil {
		t.Fatalf("调起卸载命令失败：%v", err)
	}
}

func downloadVerified(t *testing.T, item realArtifact) string {
	t.Helper()
	response, err := http.Get(item.url)
	if err != nil {
		t.Skipf("下载失败（可能是网络原因）：%v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Skipf("下载返回 %d", response.StatusCode)
	}
	target := filepath.Join(t.TempDir(), item.filename)
	file, err := os.Create(target)
	if err != nil {
		t.Fatalf("创建文件失败：%v", err)
	}
	digest := sha256.New()
	if _, err := io.Copy(io.MultiWriter(file, digest), response.Body); err != nil {
		_ = file.Close()
		t.Fatalf("写入文件失败：%v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("关闭文件失败：%v", err)
	}
	actual := hex.EncodeToString(digest.Sum(nil))
	if item.sha256 != "" && !strings.EqualFold(actual, item.sha256) {
		t.Fatalf("SHA-256 不匹配：%s", actual)
	}
	t.Logf("已下载 %s（sha256=%s）", item.filename, actual)
	return target
}

// launchProbe 启动应用并观察 2 秒：进程正常拉起即可，不强求 GUI 在无人会话里常驻。
func launchProbe(t *testing.T, path string) {
	t.Helper()
	command := exec.Command(path)
	command.Dir = filepath.Dir(path)
	if err := command.Start(); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Logf("进程已退出（%v）；文件可执行，但可能依赖桌面环境", err)
		} else {
			t.Logf("进程正常退出")
		}
	case <-time.After(2 * time.Second):
		_ = command.Process.Kill()
		<-done
		t.Logf("进程已拉起并保持运行，已结束探针进程")
	}
}

func writeZip(t *testing.T, path, name string, data []byte) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建 zip 失败：%v", err)
	}
	defer file.Close()
	writer := zip.NewWriter(file)
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
}
