#!/usr/bin/env node
// 版本唯一来源：packages/launcher/build/config.yml 的 info.version。
// ship.sh 与 CI 都通过本脚本取值，避免多处版本号漂移。
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const configPath = join(here, '..', 'packages', 'launcher', 'build', 'config.yml');

let text;
try {
  text = readFileSync(configPath, 'utf8');
} catch (error) {
  console.error(`无法读取 ${configPath}: ${error.message}`);
  process.exit(1);
}

// info 块的 version 缩进两个空格；顶层 version（Taskfile 配置版本）没有缩进，不会误匹配。
const match = text.match(/^\s{2}version:\s*["']?([^"'\s#]+)["']?/m);
if (!match) {
  console.error('无法从 build/config.yml 读取 info.version');
  process.exit(1);
}

const version = match[1];
if (!/^\d+\.\d+\.\d+$/.test(version)) {
  console.error(`info.version 不是合法版本号: ${version}`);
  process.exit(1);
}

process.stdout.write(version);
