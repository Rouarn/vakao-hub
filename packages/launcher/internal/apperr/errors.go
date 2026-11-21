// Package apperr 定义跨边界的结构化错误：稳定错误码、可重试标记与中文用户文案。
// 系统原话只放在 Cause / Op 中供日志使用，不直接展示给用户。
package apperr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

type Kind string

const (
	KindNetwork     Kind = "network"
	KindTimeout     Kind = "timeout"
	KindCanceled    Kind = "canceled"
	KindBusy        Kind = "busy"
	KindPermission  Kind = "permission"
	KindDisk        Kind = "disk"
	KindCorrupt     Kind = "corrupt"
	KindSignature   Kind = "signature"
	KindUnsupported Kind = "unsupported"
	KindServer      Kind = "server"
	KindInternal    Kind = "internal"
)

// Error 是唯一允许跨 service 边界返回的错误类型。
type Error struct {
	Kind      Kind
	Retryable bool
	Message   string
	Op        string
	Cause     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s (%s): %v", e.Message, e.Op, e.Cause)
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Op)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// New 按 Kind 的默认可重试规则构造错误；message 为空时使用该 Kind 的标准文案。
func New(kind Kind, op, message string, cause error) error {
	return NewWithRetryable(kind, kindRetryable(kind), op, message, cause)
}

func NewWithRetryable(kind Kind, retryable bool, op, message string, cause error) error {
	if message == "" {
		message = kindMessage(kind)
	}
	return &Error{Kind: kind, Retryable: retryable, Message: message, Op: op, Cause: cause}
}

// KindOf 解析错误分类；非结构化错误按 context / 网络错误推断，其余归为 internal。
func KindOf(err error) Kind {
	if err == nil {
		return KindInternal
	}
	var target *Error
	if errors.As(err, &target) {
		return target.Kind
	}
	switch {
	case errors.Is(err, context.Canceled):
		return KindCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return KindTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return KindTimeout
	}
	return KindInternal
}

// IsRetryable 返回错误是否允许用户直接重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var target *Error
	if errors.As(err, &target) {
		return target.Retryable
	}
	switch KindOf(err) {
	case KindCanceled:
		return false
	case KindTimeout, KindNetwork, KindBusy, KindCorrupt:
		return true
	default:
		return false
	}
}

// UserMessage 返回可展示给用户的中文文案；未知错误回退到 internal 文案。
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var target *Error
	if errors.As(err, &target) && target.Message != "" {
		return target.Message
	}
	return kindMessage(KindOf(err))
}

// Operation 返回内部操作名，仅用于日志。
func Operation(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Op
	}
	return ""
}

type wireError struct {
	Code      string `json:"code"`
	Kind      Kind   `json:"kind"`
	Retryable bool   `json:"retryable"`
	Message   string `json:"message"`
}

// Marshal 供 Wails application.Options.MarshalError 使用。
func Marshal(err error) []byte {
	payload := wireError{
		Code:      kindCode(KindInternal),
		Kind:      KindInternal,
		Retryable: false,
		Message:   kindMessage(KindInternal),
	}
	if err != nil {
		kind := KindOf(err)
		payload = wireError{
			Code:      kindCode(kind),
			Kind:      kind,
			Retryable: kindRetryable(kind),
			Message:   kindMessage(kind),
		}
		var target *Error
		if errors.As(err, &target) {
			if target.Message != "" {
				payload.Message = target.Message
			}
			payload.Retryable = target.Retryable
		}
	}
	data, marshalErr := json.Marshal(payload)
	if marshalErr != nil {
		return []byte(`{"code":"E_INTERNAL","kind":"internal","retryable":false,"message":"发生内部错误，请查看日志"}`)
	}
	return data
}

// FromContext 把 context / 网络错误转成 apperr；其余错误交给 FromOS。
func FromContext(err error, op string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return New(KindCanceled, op, "", err)
	case errors.Is(err, context.DeadlineExceeded):
		return New(KindTimeout, op, "", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return New(KindTimeout, op, "", err)
		}
		return New(KindNetwork, op, "", err)
	}
	return FromOS(err, op)
}

// FromHTTPStatus 根据状态码分类：429/5xx 可重试，其余 4xx 不可重试。
func FromHTTPStatus(status int, op, serverMessage string) error {
	retryable := status == 429 || status >= 500
	message := kindMessage(KindServer)
	if !retryable {
		message = "请求无效或无权限，请刷新后重试"
	}
	var cause error
	if serverMessage != "" {
		cause = errors.New(serverMessage)
	}
	return NewWithRetryable(KindServer, retryable, op, message, cause)
}

// FromOS 把 os / syscall 错误映射到权限、磁盘、占用等分类。
func FromOS(err error, op string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, context.Canceled):
		return New(KindCanceled, op, "", err)
	case errors.Is(err, context.DeadlineExceeded):
		return New(KindTimeout, op, "", err)
	case errors.Is(err, os.ErrPermission), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return New(KindPermission, op, "", err)
	case errors.Is(err, syscall.ENOSPC):
		return New(KindDisk, op, "", err)
	case errors.Is(err, syscall.EBUSY), isTextFileBusy(err), isSharingViolation(err):
		return New(KindBusy, op, "", err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return New(KindTimeout, op, "", err)
		}
		return New(KindNetwork, op, "", err)
	}
	return New(KindInternal, op, "", err)
}
