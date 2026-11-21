#!/usr/bin/env node
// 发布门禁：所有 workflow 只允许 workflow_dispatch 手动触发。
// 私有仓库 Actions 额度已用完，push / pull_request / schedule / workflow_run 会造成持续失败。
// ship.sh 在构建前调用本脚本（见 ship.sh 门禁处的注释）：先快速拦住触发方式违规，再开始编译。
// 这是额度约束的守门，不是遗留 bug，后续不要改成自动触发，也不要把调用顺序挪到构建之后。
import { readdirSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const workflowDir = join(here, '..', '.github', 'workflows');

const allowed = new Set(['workflow_dispatch']);
const problems = [];

for (const name of readdirSync(workflowDir)) {
  if (!name.endsWith('.yml') && !name.endsWith('.yaml')) continue;
  const lines = readFileSync(join(workflowDir, name), 'utf8').split('\n');

  const start = lines.findIndex((line) => /^on:\s*(.*)$/.test(line));
  if (start === -1) {
    problems.push(`${name}: 缺少 on: 块`);
    continue;
  }

  const inline = lines[start].replace(/^on:\s*/, '');
  if (inline) {
    const trigger = inline.replace(/[:#].*$/, '').trim();
    if (trigger.startsWith('[')) {
      const items = trigger
        .replace(/^\[|\]$/g, '')
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean);
      for (const item of items) {
        if (!allowed.has(item)) problems.push(`${name}: 不允许的触发方式 ${item}`);
      }
    } else if (!allowed.has(trigger)) {
      problems.push(`${name}: 不允许的触发方式 ${trigger}`);
    }
    continue;
  }

  for (let i = start + 1; i < lines.length; i += 1) {
    const line = lines[i];
    if (!line.trim() || line.trim().startsWith('#')) continue;
    // 只统计 on: 块下的二级键；遇到顶格内容说明块结束。
    if (!/^\s/.test(line)) break;
    const match = line.match(/^\s{2}([A-Za-z0-9_-]+):/);
    if (match && !allowed.has(match[1])) {
      problems.push(`${name}: 不允许的触发方式 ${match[1]}`);
    }
  }
}

if (problems.length > 0) {
  console.error('workflow 触发方式不合规：');
  for (const problem of problems) console.error(`- ${problem}`);
  process.exit(1);
}

console.log('workflow 触发方式合规：仅 workflow_dispatch');
