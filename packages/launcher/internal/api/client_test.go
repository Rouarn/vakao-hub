package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vakaohub/launcher/internal/apperr"
)

func TestCatalogHTTPStatus(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{http.StatusInternalServerError, true},
		{http.StatusNotFound, false},
		{http.StatusUnauthorized, false},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("%d", testCase.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(testCase.status)
				_, _ = w.Write([]byte(`{"code":` + fmt.Sprintf("%d", testCase.status) + `,"message":"boom"}`))
			}))
			defer server.Close()

			_, err := New(server.URL).Catalog(context.Background(), "darwin", "arm64")
			if apperr.KindOf(err) != apperr.KindServer {
				t.Fatalf("HTTP %d Kind = %s，期望 server", testCase.status, apperr.KindOf(err))
			}
			if apperr.IsRetryable(err) != testCase.retryable {
				t.Fatalf("HTTP %d retryable = %v，期望 %v", testCase.status, apperr.IsRetryable(err), testCase.retryable)
			}
			if message := apperr.UserMessage(err); message == "" || message == "boom" {
				t.Fatalf("HTTP %d 文案 = %q，期望中文用户文案", testCase.status, message)
			}
		})
	}
}

func TestCatalogBusinessErrorOnHTTP200IsNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":400,"message":"bad request"}`))
	}))
	defer server.Close()

	_, err := New(server.URL).Catalog(context.Background(), "darwin", "arm64")
	if apperr.KindOf(err) != apperr.KindServer || apperr.IsRetryable(err) {
		t.Fatalf("业务码失败 => %s retryable=%v", apperr.KindOf(err), apperr.IsRetryable(err))
	}
}

func TestCatalogMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer server.Close()

	_, err := New(server.URL).Catalog(context.Background(), "darwin", "arm64")
	if err == nil || apperr.UserMessage(err) == "" {
		t.Fatalf("坏 JSON 应返回结构化错误，实际：%v", err)
	}
}

func TestCatalogCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(server.URL).Catalog(ctx, "darwin", "arm64")
	if apperr.KindOf(err) != apperr.KindCanceled {
		t.Fatalf("取消 => %s，期望 canceled（%v）", apperr.KindOf(err), err)
	}
}

func TestCatalogTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer server.Close()

	client := New(server.URL)
	client.HTTP = &http.Client{Timeout: 30 * time.Millisecond}
	_, err := client.Catalog(context.Background(), "darwin", "arm64")
	if apperr.KindOf(err) != apperr.KindTimeout || !apperr.IsRetryable(err) {
		t.Fatalf("超时 => %s retryable=%v（%v）", apperr.KindOf(err), apperr.IsRetryable(err), err)
	}
}
