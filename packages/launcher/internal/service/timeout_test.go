package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/store"
)

func TestGetCatalogTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"code":200,"data":{"apps":[]}}`))
	}))
	defer server.Close()

	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: server.URL})
	host.timeouts.Catalog = 30 * time.Millisecond

	_, err := NewCatalog(host).GetCatalog(context.Background())
	if apperr.KindOf(err) != apperr.KindTimeout || !apperr.IsRetryable(err) {
		t.Fatalf("catalog 超时 => %s retryable=%v（%v）", apperr.KindOf(err), apperr.IsRetryable(err), err)
	}
}

func TestGetCatalogBusy(t *testing.T) {
	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: "http://127.0.0.1:1"})
	host.opMu.Lock()
	defer host.opMu.Unlock()

	_, err := NewCatalog(host).GetCatalog(context.Background())
	if apperr.KindOf(err) != apperr.KindBusy || !apperr.IsRetryable(err) {
		t.Fatalf("并发请求 => %s retryable=%v（%v）", apperr.KindOf(err), apperr.IsRetryable(err), err)
	}
}

func TestFileSHA256Timeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("写入测试文件失败：%v", err)
	}

	host := newTestHost(t, store.NewMemory())
	host.timeouts.Hash = time.Nanosecond
	_, err := NewInstall(host).fileSHA256(context.Background(), path)
	if apperr.KindOf(err) != apperr.KindTimeout {
		t.Fatalf("hash 超时 => %s（%v）", apperr.KindOf(err), err)
	}
}

func TestInstallDownloadCanceledKeepsPart(t *testing.T) {
	const totalSize = 2 << 20

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"platform": "darwin",
					"arch":     "arm64",
					"apps": []any{
						map[string]any{
							"appKey": "demo",
							"name":   "Demo",
							"latest": map[string]any{
								"id":          1,
								"versionName": "1.0.0",
								"versionCode": 1,
								"artifacts": []any{
									map[string]any{
										"id":          7,
										"platform":    "darwin",
										"arch":        "arm64",
										"format":      "zip",
										"fileName":    "demo.zip",
										"size":        totalSize,
										"sha256":      "deadbeef",
										"downloadUrl": "http://" + r.Host + "/file",
									},
								},
							},
						},
					},
				},
			})
		case "/file":
			w.Header().Set("Content-Length", strconv.Itoa(totalSize))
			chunk := make([]byte, 16*1024)
			for sent := 0; sent < totalSize; sent += len(chunk) {
				select {
				case <-r.Context().Done():
					return
				default:
				}
				if _, err := w.Write(chunk); err != nil {
					return
				}
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				time.Sleep(30 * time.Millisecond)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	host := newTestHost(t, store.NewMemory())
	host.settings.Set(Settings{BaseURL: server.URL})
	host.timeouts.Download = 30 * time.Second
	host.timeouts.DownloadStall = 5 * time.Second
	service := NewInstall(host)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		_, err := service.Install(ctx, "demo", 7)
		result <- err
	}()

	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("获取缓存目录失败：%v", err)
	}
	partPath := filepath.Join(cache, "vakaohub", "downloads", "7.part")

	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, statErr := os.Stat(partPath); statErr == nil && info.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("下载未在预期时间内开始")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()

	select {
	case err := <-result:
		if apperr.KindOf(err) != apperr.KindCanceled {
			t.Fatalf("取消下载 => %s（%v）", apperr.KindOf(err), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("取消后安装未返回")
	}

	if _, err := os.Stat(partPath); err != nil {
		t.Fatalf("取消后应保留 .part 以便续传：%v", err)
	}
}

func TestDownloadStallTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		_, _ = w.Write([]byte("first"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	host := newTestHost(t, store.NewMemory())
	host.timeouts.Download = 5 * time.Second
	host.timeouts.DownloadStall = 80 * time.Millisecond
	service := NewInstall(host)

	_, err := service.download(
		context.Background(), "op-test", "demo", server.URL, 99,
		filepath.Join(t.TempDir(), "demo.zip"), 1024,
	)
	if apperr.KindOf(err) != apperr.KindTimeout {
		t.Fatalf("无数据超时 => %s（%v）", apperr.KindOf(err), err)
	}
	if got := apperr.UserMessage(err); got != "操作超时，请重试" {
		t.Fatalf("超时文案 = %q", got)
	}
}
