package service

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/install"
	"vakaohub/launcher/internal/store"
)

// fakeBundleZIP 造一个结构合法的 darwin zip：只要求 .app/Contents/Info.plist
// 与 .app/Contents/MacOS/ 下的可执行文件，够通过发布端同款预检即可。
func fakeBundleZIP(t *testing.T, name string) []byte {
	t.Helper()
	buffer := &bytes.Buffer{}
	writer := zip.NewWriter(buffer)
	files := map[string]string{
		name + ".app/Contents/Info.plist":    "<?xml version=\"1.0\"?><plist><dict/></plist>",
		name + ".app/Contents/MacOS/" + name: "#!/bin/sh\nexit 0\n",
	}
	for path, body := range files {
		entry, err := writer.Create(path)
		if err != nil {
			t.Fatalf("构造测试 zip 失败：%v", err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("写入测试 zip 失败：%v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("关闭测试 zip 失败：%v", err)
	}
	return buffer.Bytes()
}

type fakeApp struct {
	appKey      string
	name        string
	artifactID  int
	versionName string
	versionCode int
}

// newFakeHub 起一个只服务于测试的分发服务：目录、产物下载与事件上报都在本地。
func newFakeHub(t *testing.T, apps []fakeApp) *httptest.Server {
	t.Helper()

	files := make(map[int][]byte, len(apps))
	for index, app := range apps {
		files[app.artifactID] = fakeBundleZIP(t, app.name+" "+strconv.Itoa(index))
	}

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	hashOf := func(id int) string {
		sum := sha256.Sum256(files[id])
		return hex.EncodeToString(sum[:])
	}

	mux.HandleFunc("/catalog", func(w http.ResponseWriter, r *http.Request) {
		entries := make([]map[string]any, 0, len(apps))
		for _, app := range apps {
			body := files[app.artifactID]
			entries = append(entries, map[string]any{
				"appKey": app.appKey,
				"name":   app.name,
				"latest": map[string]any{
					"id":          app.versionCode,
					"versionName": app.versionName,
					"versionCode": app.versionCode,
					"artifacts": []any{
						map[string]any{
							"id":          app.artifactID,
							"platform":    "darwin",
							"arch":        "arm64",
							"format":      "zip",
							"fileName":    app.appKey + ".zip",
							"size":        len(body),
							"sha256":      hashOf(app.artifactID),
							"downloadUrl": server.URL + "/files/" + strconv.Itoa(app.artifactID),
						},
					},
				},
			})
		}
		payload := map[string]any{
			"code": 200,
			"data": map[string]any{"platform": "darwin", "arch": "arm64", "apps": entries},
		}
		_ = json.NewEncoder(w).Encode(payload)
	})

	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/files/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		body, ok := files[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
	})

	mux.HandleFunc("/updates/report", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":null}`))
	})

	return server
}

// overrideInstallPackage 替换真正的落盘安装；测试结束自动还原。
func overrideInstallPackage(t *testing.T, fn func(context.Context, install.Options) (install.Result, error)) {
	t.Helper()
	original := installPackage
	t.Cleanup(func() { installPackage = original })
	installPackage = fn
}

// stubNoRunningProcess 跳过 ps 扫描，避免测试环境干扰。
func stubNoRunningProcess(t *testing.T) {
	t.Helper()
	original := runningProcesses
	t.Cleanup(func() { runningProcesses = original })
	runningProcesses = func(string) ([]install.RunningProcess, error) { return nil, nil }
}

// newConcurrencyHost 构造指向假分发服务的 Host。
func newConcurrencyHost(t *testing.T, server *httptest.Server) *Host {
	t.Helper()
	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: server.URL, DeviceID: "test-device"})
	return host
}

// TestInstallDifferentAppsConcurrently 验证两个应用可以同时安装：
// 两个安装必须同时停在安装阶段，而不是第二个立刻拿到 busy。
func TestInstallDifferentAppsConcurrently(t *testing.T) {
	stubNoRunningProcess(t)

	apps := []fakeApp{
		{appKey: "alpha", name: "Concurrent Alpha", artifactID: 71, versionName: "1.0.1", versionCode: 101},
		{appKey: "beta", name: "Concurrent Beta", artifactID: 72, versionName: "1.0.1", versionCode: 101},
	}
	server := newFakeHub(t, apps)
	host := newConcurrencyHost(t, server)
	service := NewInstall(host)

	var (
		mu       sync.Mutex
		started  int
		bothOpen = make(chan struct{})
	)
	overrideInstallPackage(t, func(ctx context.Context, opts install.Options) (install.Result, error) {
		mu.Lock()
		started++
		if started == 2 {
			close(bothOpen)
		}
		mu.Unlock()

		select {
		case <-bothOpen:
		case <-ctx.Done():
			return install.Result{}, apperr.FromContext(ctx.Err(), "install")
		case <-time.After(10 * time.Second):
			return install.Result{}, apperr.New(apperr.KindInternal, "test.overlap", "两个安装没有重叠", nil)
		}
		return install.Result{Path: filepath.Join(t.TempDir(), opts.AppKey+".app"), Kind: install.KindPortable}, nil
	})

	results := make(chan error, len(apps))
	for _, app := range apps {
		go func(app fakeApp) {
			_, err := service.Install(context.Background(), app.appKey, app.artifactID)
			results <- err
		}(app)
	}

	for range apps {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("并发安装失败：%v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("并发安装未在预期时间内完成（可能仍被全局锁串行化）")
		}
	}

	if installed := host.store.List(); len(installed) != len(apps) {
		t.Fatalf("应记录 %d 个已安装应用，实际 %+v", len(apps), installed)
	}
}

// TestInstallSameAppRejected 验证同一个应用重复安装会被拒绝，
// 且文案是「该应用正在更新」，不再误报「应用正在运行」。
func TestInstallSameAppRejected(t *testing.T) {
	stubNoRunningProcess(t)

	apps := []fakeApp{
		{appKey: "alpha", name: "Concurrent Alpha", artifactID: 81, versionName: "1.0.1", versionCode: 101},
	}
	server := newFakeHub(t, apps)
	host := newConcurrencyHost(t, server)
	service := NewInstall(host)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once, releaseOnce sync.Once
	overrideInstallPackage(t, func(ctx context.Context, opts install.Options) (install.Result, error) {
		once.Do(func() { close(entered) })
		select {
		case <-release:
		case <-ctx.Done():
			return install.Result{}, apperr.FromContext(ctx.Err(), "install")
		}
		return install.Result{Path: filepath.Join(t.TempDir(), "alpha.app"), Kind: install.KindPortable}, nil
	})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	first := make(chan error, 1)
	go func() {
		_, err := service.Install(context.Background(), "alpha", 81)
		first <- err
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("首次安装未进入安装阶段")
	}

	_, err := service.Install(context.Background(), "alpha", 81)
	if apperr.KindOf(err) != apperr.KindBusy || !apperr.IsRetryable(err) {
		t.Fatalf("同一应用并发安装 => %s retryable=%v（%v）", apperr.KindOf(err), apperr.IsRetryable(err), err)
	}
	if message := apperr.UserMessage(err); !strings.Contains(message, "该应用正在更新") {
		t.Fatalf("并发安装文案不正确：%s", message)
	}

	releaseOnce.Do(func() { close(release) })
	if err := <-first; err != nil {
		t.Fatalf("首次安装失败：%v", err)
	}
}

// TestPendingArtifactIsPerApp 验证「等待用户选择安装方式」的保留包按应用分开，
// 两个应用先后进入选择流程时不会互相覆盖。
func TestPendingArtifactIsPerApp(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "alpha.exe")
	second := filepath.Join(dir, "beta.exe")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("stub"), 0o644); err != nil {
			t.Fatalf("写入保留包失败：%v", err)
		}
	}

	service := NewInstall(newTestHost(t, store.NewMemory()))
	service.rememberPending("alpha", 1, "exe", first)
	service.rememberPending("beta", 2, "exe", second)

	if got := service.resumePending("alpha", 1); got != first {
		t.Fatalf("alpha 保留包 = %q，期望 %q", got, first)
	}
	if got := service.resumePending("beta", 2); got != second {
		t.Fatalf("beta 保留包 = %q，期望 %q", got, second)
	}
	if got := service.resumePending("alpha", 1); got != "" {
		t.Fatalf("保留包应已被消费，实际 %q", got)
	}
	// 产物 ID 不匹配时不能复用别的包。
	service.rememberPending("alpha", 3, "exe", first)
	if got := service.resumePending("alpha", 4); got != "" {
		t.Fatalf("产物不匹配时不应复用，实际 %q", got)
	}
}

// TestBeginAppOpIndependentApps 是并发原语的直接单元测试：不同应用互不阻塞。
func TestBeginAppOpIndependentApps(t *testing.T) {
	host := newTestHost(t, store.NewMemory())

	_, releaseAlpha, err := host.beginAppOp("alpha")
	if err != nil {
		t.Fatalf("占用 alpha 失败：%v", err)
	}
	defer releaseAlpha()

	_, releaseBeta, err := host.beginAppOp("beta")
	if err != nil {
		t.Fatalf("不同应用不应互相阻塞：%v", err)
	}
	releaseBeta()

	if _, _, err := host.beginAppOp("alpha"); apperr.KindOf(err) != apperr.KindBusy {
		t.Fatalf("同一应用应返回 busy，实际 %v", err)
	}

	releaseAlpha()
	_, releaseAgain, err := host.beginAppOp("alpha")
	if err != nil {
		t.Fatalf("释放后应能再次占用：%v", err)
	}
	releaseAgain()
}

// TestBeginDialogExclusive 验证系统选择器仍然全局串行。
func TestBeginDialogExclusive(t *testing.T) {
	host := newTestHost(t, store.NewMemory())

	release, err := host.beginDialog()
	if err != nil {
		t.Fatalf("首次打开选择器失败：%v", err)
	}
	if _, err := host.beginDialog(); apperr.KindOf(err) != apperr.KindBusy {
		t.Fatalf("第二个选择器应返回 busy，实际 %v", err)
	}
	release()
	releaseAgain, err := host.beginDialog()
	if err != nil {
		t.Fatalf("释放后应能再次打开选择器：%v", err)
	}
	releaseAgain()
}
