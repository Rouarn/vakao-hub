// 卡片内联提示：安装失败、运行状态未知、自更新结果这类反馈都贴在对应应用卡片里，
// 不再占用列表顶部的整条横幅。
export type CardNoteKind = 'error' | 'warning' | 'info';

export interface CardNote {
  kind: CardNoteKind;
  message: string;
  /** 可重试的失败把它放进来，卡片上会出现一个轻量的「重试」。 */
  retry?: () => unknown;
}
