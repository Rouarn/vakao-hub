package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"vakaohub/launcher/internal/api"
	"vakaohub/launcher/internal/apperr"
	"vakaohub/launcher/internal/contract"
)

// downloadClient 不设全局 Timeout：总时长由 context 控制，卡死由读循环的 stall 检测处理。
var downloadClient = &http.Client{}

// ensureMinOS 在下载前拦截低于产物最低系统要求的 macOS。
func (s *InstallService) ensureMinOS(appKey, opID string, artifact *api.Artifact) error {
	if artifact.Platform != contract.PlatformDarwin || artifact.MinOs == "" {
		return nil
	}
	current := hostOSVersion()
	if current == "" || !isNewerVersion(artifact.MinOs, current) {
		return nil
	}
	err := apperr.New(
		apperr.KindUnsupported,
		"install.minos",
		fmt.Sprintf("该版本需要 macOS %s 或更高版本，当前系统为 %s", artifact.MinOs, current),
		nil,
	)
	s.fail(appKey, opID, err)
	return err
}

func (s *InstallService) download(ctx context.Context, opID, appKey, url string, artifactID int, dest string, expectedSize int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", apperr.FromContext(err, "download")
	}
	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.Download)
	defer cancel()

	cache, err := os.UserCacheDir()
	if err != nil {
		return "", apperr.FromOS(err, "download.cache")
	}
	directory := filepath.Join(cache, "vakaohub", "downloads")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", apperr.FromOS(err, "download.cache")
	}
	partPath := filepath.Join(directory, fmt.Sprintf("%d.part", artifactID))

	var start int64
	if info, err := os.Stat(partPath); err == nil {
		start = info.Size()
	}
	if expectedSize > 0 && start > expectedSize {
		start = 0
		_ = os.Remove(partPath)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", apperr.New(apperr.KindInternal, "download.request", "下载地址无效，请刷新后重试", err)
	}
	if start > 0 {
		request.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}

	response, err := downloadClient.Do(request)
	if err != nil {
		return "", apperr.FromContext(err, "download")
	}
	defer response.Body.Close()

	complete := false
	switch response.StatusCode {
	case http.StatusPartialContent:
		// 续传：保留已有部分，追加写入
	case http.StatusOK:
		// 服务端不支持 Range 或文件已变化，从头开始
		start = 0
	case http.StatusRequestedRangeNotSatisfiable:
		if expectedSize > 0 && start == expectedSize {
			complete = true
		} else {
			return "", apperr.FromHTTPStatus(response.StatusCode, "download", "")
		}
	default:
		return "", apperr.FromHTTPStatus(response.StatusCode, "download", "")
	}

	if !complete {
		flags := os.O_CREATE | os.O_WRONLY
		if start > 0 {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}
		file, err := os.OpenFile(partPath, flags, 0o644)
		if err != nil {
			return "", apperr.FromOS(err, "download")
		}

		total := expectedSize
		if total <= 0 && response.ContentLength > 0 {
			total = start + response.ContentLength
		}
		received := start
		lastPercent := -1
		lastEmit := time.Now()
		buffer := make([]byte, 128*1024)
		guard := newStallGuard(response.Body, s.host.timeouts.DownloadStall)
		defer guard.Stop()

		for {
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return "", apperr.FromContext(err, "download")
			}
			n, readErr := guard.Read(buffer)
			if n > 0 {
				if _, err := file.Write(buffer[:n]); err != nil {
					_ = file.Close()
					return "", apperr.FromOS(err, "download")
				}
				received += int64(n)
				percent := -1
				if total > 0 {
					percent = int(received * 100 / total)
					if percent > 100 {
						percent = 100
					}
				}
				if percent != lastPercent && time.Since(lastEmit) > 80*time.Millisecond {
					lastPercent = percent
					lastEmit = time.Now()
					s.host.emit(appKey, opID, "downloading", percent, "下载中", false)
				}
			}
			if readErr == io.EOF {
				guard.Stop()
				break
			}
			if readErr != nil {
				guard.Stop()
				_ = file.Close()
				if guard.TimedOut() {
					return "", apperr.New(apperr.KindTimeout, "download.stall", "", readErr)
				}
				return "", apperr.FromContext(readErr, "download")
			}
		}
		if err := file.Close(); err != nil {
			return "", apperr.FromOS(err, "download")
		}
	}

	if expectedSize > 0 {
		info, err := os.Stat(partPath)
		if err != nil {
			return "", apperr.FromOS(err, "download")
		}
		if info.Size() != expectedSize {
			return "", apperr.New(apperr.KindCorrupt, "download.size", "下载不完整，请重试", nil)
		}
	}

	s.host.emit(appKey, opID, "downloading", 100, "下载完成", false)
	sha, err := s.fileSHA256(ctx, partPath)
	if err != nil {
		return "", err
	}
	if err := os.Rename(partPath, dest); err != nil {
		return "", apperr.FromOS(err, "download")
	}
	return sha, nil
}

func (s *InstallService) fileSHA256(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.host.timeouts.Hash)
	defer cancel()

	file, err := os.Open(path)
	if err != nil {
		return "", apperr.FromOS(err, "verify")
	}
	defer file.Close()

	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", apperr.FromContext(err, "verify")
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", apperr.FromOS(readErr, "verify")
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// stallGuard 在连续一段时间没有收到数据时关闭 body，把“连接还在但不动”转成超时。
type stallGuard struct {
	body     io.ReadCloser
	timeout  time.Duration
	timer    *time.Timer
	timedOut atomic.Bool
}

func newStallGuard(body io.ReadCloser, timeout time.Duration) *stallGuard {
	guard := &stallGuard{body: body, timeout: timeout}
	if timeout > 0 {
		guard.timer = time.AfterFunc(timeout, func() {
			guard.timedOut.Store(true)
			_ = body.Close()
		})
	}
	return guard
}

func (g *stallGuard) Read(p []byte) (int, error) {
	n, err := g.body.Read(p)
	if n > 0 && g.timer != nil {
		g.timer.Reset(g.timeout)
	}
	return n, err
}

func (g *stallGuard) Stop() {
	if g.timer != nil {
		g.timer.Stop()
	}
}

func (g *stallGuard) TimedOut() bool {
	return g.timedOut.Load()
}
