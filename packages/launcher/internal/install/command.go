package install

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"vakaohub/launcher/internal/apperr"
)

// extractTimeout 是解压、挂载复制与安装器执行的默认上限。
const extractTimeout = 10 * time.Minute

// commandFailure 把 exec 的退出状态与输出收进 Cause，只把中文文案交给用户。
func commandFailure(ctx context.Context, kind apperr.Kind, op, message string, cause error, output []byte) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return apperr.FromContext(err, op)
		}
	}
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		cause = fmt.Errorf("%w: %s", cause, detail)
	}
	if kind == apperr.KindInternal {
		kind = classifyOutput(detail, kind)
	}
	return apperr.New(kind, op, message, cause)
}

func classifyOutput(output string, fallback apperr.Kind) apperr.Kind {
	lower := strings.ToLower(output)
	switch {
	case strings.Contains(lower, "no space left"):
		return apperr.KindDisk
	case strings.Contains(lower, "permission denied"):
		return apperr.KindPermission
	case strings.Contains(lower, "resource busy"), strings.Contains(lower, "text file busy"):
		return apperr.KindBusy
	}
	return fallback
}

// copyWithContext 让大文件复制在取消时尽快退出，避免继续写半成品。
func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buffer := make([]byte, 128*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := src.Read(buffer)
		if read > 0 {
			if _, writeErr := dst.Write(buffer[:read]); writeErr != nil {
				return writeErr
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func contextOf(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
