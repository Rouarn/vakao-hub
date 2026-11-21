package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"
)

type wire struct {
	Code      string `json:"code"`
	Kind      Kind   `json:"kind"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}

func decode(t *testing.T, err error) wire {
	t.Helper()
	var payload wire
	if unmarshalErr := json.Unmarshal(Marshal(err), &payload); unmarshalErr != nil {
		t.Fatalf("Marshal 输出非法 JSON：%v", unmarshalErr)
	}
	return payload
}

func TestKindTable(t *testing.T) {
	cases := []struct {
		kind       Kind
		code       string
		retryable  bool
		userPhrase string
	}{
		{KindNetwork, "E_NETWORK", true, "无法连接更新服务"},
		{KindTimeout, "E_TIMEOUT", true, "操作超时"},
		{KindCanceled, "E_CANCELED", false, "已取消"},
		{KindBusy, "E_BUSY", true, "应用正在运行"},
		{KindPermission, "E_PERMISSION", false, "没有写入权限"},
		{KindDisk, "E_DISK", false, "磁盘空间不足"},
		{KindCorrupt, "E_CORRUPT", true, "安装包损坏"},
		{KindSignature, "E_SIGNATURE", false, "签名校验失败"},
		{KindUnsupported, "E_UNSUPPORTED", false, "不支持自动安装"},
		{KindServer, "E_SERVER", true, "服务暂时不可用"},
		{KindInternal, "E_INTERNAL", false, "内部错误"},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.kind), func(t *testing.T) {
			err := New(testCase.kind, "test", "", errors.New("raw"))
			payload := decode(t, err)
			if payload.Code != testCase.code || payload.Kind != testCase.kind {
				t.Fatalf("code/kind = %s/%s，期望 %s/%s", payload.Code, payload.Kind, testCase.code, testCase.kind)
			}
			if payload.Retryable != testCase.retryable || IsRetryable(err) != testCase.retryable {
				t.Fatalf("%s retryable = %v/%v，期望 %v", testCase.kind, payload.Retryable, IsRetryable(err), testCase.retryable)
			}
			if !contains(UserMessage(err), testCase.userPhrase) {
				t.Fatalf("%s 文案 = %q，期望包含 %q", testCase.kind, UserMessage(err), testCase.userPhrase)
			}
		})
	}
}

func TestHTTPStatusMapping(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
		phrase    string
	}{
		{500, true, "服务暂时不可用"},
		{502, true, "服务暂时不可用"},
		{429, true, "服务暂时不可用"},
		{404, false, "请求无效或无权限"},
		{401, false, "请求无效或无权限"},
		{400, false, "请求无效或无权限"},
	}
	for _, testCase := range cases {
		err := FromHTTPStatus(testCase.status, "catalog", "")
		payload := decode(t, err)
		if payload.Code != "E_SERVER" || payload.Retryable != testCase.retryable {
			t.Fatalf("HTTP %d => %s/%v，期望 E_SERVER/%v", testCase.status, payload.Code, payload.Retryable, testCase.retryable)
		}
		if !contains(payload.Message, testCase.phrase) {
			t.Fatalf("HTTP %d 文案 = %q，期望包含 %q", testCase.status, payload.Message, testCase.phrase)
		}
	}
}

func TestFromContext(t *testing.T) {
	canceled := FromContext(context.Canceled, "download")
	if KindOf(canceled) != KindCanceled || IsRetryable(canceled) {
		t.Fatalf("context.Canceled => %s retryable=%v", KindOf(canceled), IsRetryable(canceled))
	}

	deadline := FromContext(context.DeadlineExceeded, "download")
	if KindOf(deadline) != KindTimeout || !IsRetryable(deadline) {
		t.Fatalf("context.DeadlineExceeded => %s retryable=%v", KindOf(deadline), IsRetryable(deadline))
	}

	network := FromContext(&net.DNSError{Err: "no such host", Name: "example.invalid"}, "catalog")
	if KindOf(network) != KindNetwork || !IsRetryable(network) {
		t.Fatalf("DNS 错误 => %s retryable=%v", KindOf(network), IsRetryable(network))
	}

	timeout := FromContext(&net.DNSError{Err: "timeout", Name: "example.invalid", IsTimeout: true}, "catalog")
	if KindOf(timeout) != KindTimeout {
		t.Fatalf("DNS 超时 => %s", KindOf(timeout))
	}
}

func TestFromOS(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Kind
	}{
		{"permission", &os.PathError{Op: "open", Path: "x", Err: syscall.EACCES}, KindPermission},
		{"disk", &os.PathError{Op: "write", Path: "x", Err: syscall.ENOSPC}, KindDisk},
		{"busy", &os.PathError{Op: "rename", Path: "x", Err: syscall.EBUSY}, KindBusy},
		{"unknown", errors.New("boom"), KindInternal},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := FromOS(testCase.err, "install")
			if got := KindOf(err); got != testCase.want {
				t.Fatalf("FromOS => %s，期望 %s", got, testCase.want)
			}
		})
	}
}

// 文件系统/进程错误会实现 net.Error（syscall.Errno 带 Timeout/Temporary），
// 必须锁住它们不会被归到网络类，否则界面会把本地磁盘问题说成“无法连接更新服务”。
func TestLocalErrorsNeverNetwork(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"file-not-exist", &os.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}},
		{"cross-device", &os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.Errno(17)}},
		{"invalid-argument", &os.PathError{Op: "read", Path: "x", Err: syscall.EINVAL}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if kind := KindOf(FromOS(testCase.err, "install")); kind == KindNetwork || kind == KindTimeout {
				t.Fatalf("FromOS 把本地错误归为 %s：%v", kind, testCase.err)
			}
			if kind := KindOf(FromContext(testCase.err, "download")); kind == KindNetwork || kind == KindTimeout {
				t.Fatalf("FromContext 把本地错误归为 %s：%v", kind, testCase.err)
			}
			if kind := KindOf(testCase.err); kind == KindNetwork || kind == KindTimeout {
				t.Fatalf("KindOf 把本地错误归为 %s：%v", kind, testCase.err)
			}
		})
	}
}

func TestNetworkErrorsStillNetwork(t *testing.T) {
	network := FromContext(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")}, "catalog")
	if KindOf(network) != KindNetwork {
		t.Fatalf("网络错误 => %s，期望 network", KindOf(network))
	}

	timeout := FromContext(&net.OpError{Op: "read", Net: "tcp", Err: timeoutError{}}, "catalog")
	if KindOf(timeout) != KindTimeout {
		t.Fatalf("网络超时 => %s，期望 timeout", KindOf(timeout))
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestMarshalFallback(t *testing.T) {
	nilPayload := decode(t, nil)
	if nilPayload.Code != "E_INTERNAL" || nilPayload.Message == "" {
		t.Fatalf("nil Marshal = %+v", nilPayload)
	}

	plainPayload := decode(t, errors.New("raw English"))
	if plainPayload.Code != "E_INTERNAL" || plainPayload.Message != "发生内部错误，请查看日志" {
		t.Fatalf("普通错误 Marshal = %+v", plainPayload)
	}
	if contains(plainPayload.Message, "raw English") {
		t.Fatalf("Marshal 不应把系统原话放进 message：%q", plainPayload.Message)
	}
}

func TestCustomMessage(t *testing.T) {
	err := New(KindUnsupported, "install", "该版本需要 macOS 13.0 或更高版本", nil)
	if UserMessage(err) != "该版本需要 macOS 13.0 或更高版本" {
		t.Fatalf("自定义文案被覆盖：%q", UserMessage(err))
	}
}

func contains(value, part string) bool {
	return len(part) == 0 || (len(value) >= len(part) && indexOf(value, part) >= 0)
}

func indexOf(value, part string) int {
	for index := 0; index+len(part) <= len(value); index++ {
		if value[index:index+len(part)] == part {
			return index
		}
	}
	return -1
}
