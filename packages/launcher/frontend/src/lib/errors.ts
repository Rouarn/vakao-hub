export interface UserError {
  code: string;
  kind: string;
  retryable: boolean;
  message: string;
}

// 解析失败时的兜底文案：不把原始对象或英文系统原话展示给用户。
export const FALLBACK_ERROR: UserError = {
  code: 'E_INTERNAL',
  kind: 'internal',
  retryable: false,
  message: '操作失败，请稍后重试',
};

const CANCELED_ERROR: UserError = {
  code: 'E_CANCELED',
  kind: 'canceled',
  retryable: false,
  message: '已取消',
};

function parseJson(value: string): unknown {
  try {
    return JSON.parse(value);
  } catch {
    return undefined;
  }
}

function fromRecord(record: Record<string, unknown>): UserError | null {
  const kind = typeof record.kind === 'string' ? record.kind : '';
  const code = typeof record.code === 'string' ? record.code : '';
  // 只接受结构化错误，避免把 Error.message 里的系统原话当成用户文案。
  if (!kind && !code) return null;

  const message = typeof record.message === 'string' ? record.message : '';
  return {
    code: code || 'E_INTERNAL',
    kind: kind || 'internal',
    retryable: record.retryable === true,
    message: kind === 'canceled' ? '已取消' : message || FALLBACK_ERROR.message,
  };
}

function fromUnknown(value: unknown, depth = 0): UserError | null {
  if (value == null || depth > 3) return null;
  if (typeof value === 'object') {
    return fromRecord(value as Record<string, unknown>);
  }
  if (typeof value !== 'string') return null;

  const parsed = parseJson(value);
  if (parsed === undefined) return null;
  if (typeof parsed === 'string') return fromUnknown(parsed, depth + 1);
  return fromUnknown(parsed, depth + 1);
}

function fromRawMessage(message: string): UserError | null {
  const lower = message.toLowerCase();
  // Wails 的 CancellablePromise.cancel() 会直接 reject CancelError("Promise cancelled.")，
  // 不会等到 Go 侧返回结构化错误。
  if (lower.includes('promise canceled') || lower.includes('promise cancelled')) {
    return { ...CANCELED_ERROR };
  }
  // Wails 对取消调用的拒绝可能绕过 MarshalError，直接给 apperr.Error() 的文本。
  if (lower.includes('context canceled') || lower.includes('context cancelled') || message.includes('已取消')) {
    return { ...CANCELED_ERROR };
  }
  if (lower.includes('context deadline exceeded') || message.includes('操作超时')) {
    return { code: 'E_TIMEOUT', kind: 'timeout', retryable: true, message: '操作超时，请重试' };
  }
  return null;
}

/**
 * 把 Wails 绑定调用抛出的错误解析为统一结构。
 * Wails v3 会把 MarshalError 的 JSON 放进 Error.message，个别情况下还会二次编码。
 */
export function toUserError(error: unknown): UserError {
  if (error instanceof Error) {
    if (error.name === 'CancelError') return { ...CANCELED_ERROR };

    const fromMessage = fromUnknown(error.message);
    if (fromMessage) return fromMessage;

    const withCause = error as Error & { cause?: unknown };
    const fromCause = fromUnknown(withCause.cause);
    if (fromCause) return fromCause;
  }

  const direct = fromUnknown(error);
  if (direct) return direct;

  const raw = error instanceof Error ? error.message : typeof error === 'string' ? error : '';
  const fromRaw = fromRawMessage(raw);
  if (fromRaw) return fromRaw;
  return { ...FALLBACK_ERROR };
}
