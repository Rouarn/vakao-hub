#!/usr/bin/env node
// 版本与产品元数据的唯一来源：packages/launcher/build/config.yml 的 info 块。
// ship.sh、Taskfile 与 CI 都通过本脚本取值，避免多处手改漂移。
//
// 用法：
//   node scripts/version.mjs                     # 输出 info.version
//   node scripts/version.mjs --code              # 输出 versionCode
//   node scripts/version.mjs productIdentifier   # 输出 info 下任意字段
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const CONFIG_PATH = join(
  dirname(fileURLToPath(import.meta.url)),
  '..',
  'packages',
  'launcher',
  'build',
  'config.yml',
);

/** 去掉 YAML 标量的引号与行尾注释；只处理本项目用到的单行标量。 */
export function yamlScalar(raw) {
  const value = raw.trim();
  if (!value) return '';
  const quote = value[0];
  if (quote === '"' || quote === "'") {
    const end = value.indexOf(quote, 1);
    if (end === -1) throw new Error(`YAML 标量引号未闭合: ${value}`);
    return value.slice(1, end);
  }
  return value.replace(/\s+#.*$/, '').trim();
}

/**
 * 解析 config.yml 的 info 块。
 * 不依赖固定缩进宽度：只在顶层 `info:` 之后、下一个顶层键之前取二级键，
 * 因此重排缩进或增加注释都不会读错版本。
 */
export function parseInfo(text) {
  const info = {};
  let inInfo = false;
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.replace(/\s+$/, '');
    if (!line.trim() || line.trim().startsWith('#')) continue;

    const topLevel = /^([A-Za-z0-9_-]+):(?:\s*(.*))?$/.exec(line);
    if (topLevel) {
      inInfo = topLevel[1] === 'info';
      continue;
    }
    if (!inInfo) continue;

    const entry = /^\s+([A-Za-z0-9_-]+):\s*(.*)$/.exec(line);
    if (entry) info[entry[1]] = yamlScalar(entry[2]);
  }
  return info;
}

export function readInfo(path = CONFIG_PATH) {
  let text;
  try {
    text = readFileSync(path, 'utf8');
  } catch (error) {
    throw new Error(`无法读取 ${path}: ${error.message}`, { cause: error });
  }
  const info = parseInfo(text);
  if (!info.version) throw new Error('无法从 build/config.yml 读取 info.version');
  return info;
}

/**
 * 单调递增的 versionCode：major*1_000_000 + minor*1_000 + patch。
 * minor / patch 各留三位，避免旧的 major*100+minor*10+patch 在两位数时撞号。
 */
export function versionCode(version) {
  const match = /^(\d+)\.(\d+)\.(\d+)$/.exec(version);
  if (!match) throw new Error(`不是合法版本号: ${version}`);
  const [major, minor, patch] = match.slice(1).map(Number);
  if (minor > 999 || patch > 999) {
    throw new Error(`版本号 minor / patch 不能超过 999: ${version}`);
  }
  return major * 1_000_000 + minor * 1_000 + patch;
}

function main(argv) {
  const info = readInfo();
  const arg = argv[0];
  if (!arg) {
    process.stdout.write(info.version);
    return;
  }
  if (arg === '--code') {
    process.stdout.write(String(versionCode(info.version)));
    return;
  }
  if (arg.startsWith('--')) throw new Error(`未知参数: ${arg}`);
  if (!info[arg]) throw new Error(`config.yml 的 info 块没有字段 ${arg}`);
  process.stdout.write(info[arg]);
}

const isMain = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isMain) {
  try {
    main(process.argv.slice(2));
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}
