/**
 * 自实现窗口拖动。
 *
 * 不用 Wails 的 `--wails-draggable`：WKWebView 下它依赖 mousedown 的 detail/buttons
 * 与原生事件严格匹配，实际表现不稳定（表现为按下后光标变成文本 I 型、窗口不动）。
 * 这里直接监听 mousedown + mousemove，用 runtime 的窗口定位 API 移动窗口。
 */

import { Window } from '@wailsio/runtime';

const NO_DRAG_SELECTOR = 'button, a, input, select, textarea, label, [contenteditable="true"], [data-no-drag]';

export function useWindowDrag() {
  let dragging = false;
  let startX = 0;
  let startY = 0;
  let baseX = 0;
  let baseY = 0;
  let frame = 0;
  let nextX = 0;
  let nextY = 0;

  async function onMouseDown(event: MouseEvent): Promise<void> {
    if (event.button !== 0) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest(NO_DRAG_SELECTOR)) return;

    try {
      const position = await Window.Position();
      baseX = position.x;
      baseY = position.y;
    } catch {
      return;
    }

    startX = event.screenX;
    startY = event.screenY;
    dragging = true;
    document.body.classList.add('is-window-dragging');
    window.addEventListener('mousemove', onMouseMove);
    window.addEventListener('mouseup', onMouseUp, { once: true });
    // 阻止文本选中（否则光标会变成 I 型）
    event.preventDefault();
  }

  function onMouseMove(event: MouseEvent): void {
    if (!dragging) return;
    nextX = Math.round(baseX + (event.screenX - startX));
    nextY = Math.round(baseY + (event.screenY - startY));
    if (frame) return;
    frame = window.requestAnimationFrame(() => {
      frame = 0;
      Window.SetPosition(nextX, nextY).catch(() => undefined);
    });
  }

  function onMouseUp(): void {
    dragging = false;
    document.body.classList.remove('is-window-dragging');
    window.removeEventListener('mousemove', onMouseMove);
    if (frame) {
      window.cancelAnimationFrame(frame);
      frame = 0;
    }
  }

  return { onMouseDown };
}
