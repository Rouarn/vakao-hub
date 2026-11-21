#!/usr/bin/env node
/**
 * vakao-hub 发布 CLI：构建完一条命令把产物推到软件中心。
 *
 * 用法：
 *   node tools/hub-cli.mjs apps
 *   node tools/hub-cli.mjs auth-check
 *   node tools/hub-cli.mjs release --app dockdeck --version 1.0.0 --code 1000000 \
 *     --file ./DockDeck.zip --platform darwin --arch arm64 --format zip --min-os 12.0 \
 *     --notes "1.0.0 首发" [--publish full|gray|none] [--gray-percent 10]
 *   node tools/hub-cli.mjs status --app dockdeck --version 1.0.0 [--code 1000000] [--expect full|gray]
 *   node tools/hub-cli.mjs stage --app dockdeck --version 1.0.0
 *   node tools/hub-cli.mjs publish --app dockdeck --version 1.0.0 [--code 1000000] [--publish full|gray]
 *   node tools/hub-cli.mjs verify --app dockdeck --version 1.0.0 --code 1000000 \
 *     --expect full --platforms darwin,win32
 *
 *   release 支持 --replace：同平台/架构/格式已存在时先删旧产物再上传，便于失败后重跑。
 *
 * 认证（优先级从高到低）：
 *   --token-file <path> / VAKAO_HUB_TOKEN
 *   或 VAKAO_HUB_USER + VAKAO_HUB_PASS（登录后缓存到 ~/.cache/vakao-hub/token.json）
 *
 * 环境变量：
 *   VAKAO_HUB_URL  默认 https://dist.woyuxiaji.cn/api/v1
 */
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join, basename } from 'node:path';
import { createHash } from 'node:crypto';
import { pathToFileURL } from 'node:url';
import { versionCode } from '../scripts/version.mjs';

const BASE = process.env.VAKAO_HUB_URL || 'https://dist.woyuxiaji.cn/api/v1';
const CACHE = join(homedir(), '.cache', 'vakao-hub', 'token.json');

export const PUBLISH_MODES = ['none', 'gray', 'full'];
const EXPECTED_STATUS = { full: 2, gray: 1 };

export function parseArgs(argv) {
  const out = { _: [], artifacts: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (!a.startsWith('--')) {
      out._.push(a);
      continue;
    }
    if (a.includes('=')) {
      throw new Error(`不支持等号写法 ${a}，请用 "--key value"`);
    }
    const key = a.slice(2);
    const next = argv[i + 1];
    if (key === 'no-publish') {
      out.publish = 'none';
      continue;
    }
    if (next === undefined || next.startsWith('--')) {
      out[key] = true;
      continue;
    }
    if (key === 'file') {
      out.artifacts.push({ file: next });
      i += 1;
      continue;
    }
    const last = out.artifacts[out.artifacts.length - 1];
    if (last && ['platform', 'arch', 'format', 'min-os'].includes(key)) {
      last[key === 'min-os' ? 'minOs' : key] = next;
    } else {
      out[key] = next;
    }
    i += 1;
  }
  return out;
}

/** 严格校验发布模式，任何拼写错误都必须报错，不能静默升级为全量。 */
export function resolvePublishMode(args) {
  const mode = args.publish ?? 'full';
  if (typeof mode !== 'string' || !PUBLISH_MODES.includes(mode)) {
    throw new Error(`--publish 只接受 ${PUBLISH_MODES.join('/')}，收到: ${JSON.stringify(mode)}`);
  }
  return mode;
}

function cacheToken() {
  if (!existsSync(CACHE)) return null;
  try {
    const cached = JSON.parse(readFileSync(CACHE, 'utf8'));
    if (cached.expiresAt && Date.now() < cached.expiresAt) return cached.token;
  } catch {
    return null;
  }
  return null;
}

/** 只检查凭据来源，不发请求；用于构建前快速失败。 */
export function hasCredentials(args) {
  if (args['token-file']) return existsSync(args['token-file']);
  if (process.env.VAKAO_HUB_TOKEN) return true;
  if (cacheToken()) return true;
  return Boolean(process.env.VAKAO_HUB_USER && process.env.VAKAO_HUB_PASS);
}

async function token(args) {
  if (args['token-file']) return readFileSync(args['token-file'], 'utf8').trim();
  if (process.env.VAKAO_HUB_TOKEN) return process.env.VAKAO_HUB_TOKEN.trim();
  const cached = cacheToken();
  if (cached) return cached;
  const username = process.env.VAKAO_HUB_USER;
  const password = process.env.VAKAO_HUB_PASS;
  if (!username || !password) {
    throw new Error('缺少凭据：设置 VAKAO_HUB_TOKEN，或 VAKAO_HUB_USER + VAKAO_HUB_PASS');
  }
  const res = await api('POST', '/admin/auth/login', { username, password }, null);
  const t = res?.data?.token ?? res?.data?.accessToken;
  if (!t) throw new Error('登录失败：' + JSON.stringify(res).slice(0, 200));
  mkdirSync(join(homedir(), '.cache', 'vakao-hub'), { recursive: true });
  writeFileSync(CACHE, JSON.stringify({ token: t, expiresAt: Date.now() + 6 * 3600 * 1000 }), { mode: 0o600 });
  return t;
}

async function api(method, path, body, tok) {
  const headers = { 'Content-Type': 'application/json' };
  if (tok) headers.Authorization = `Bearer ${tok}`;
  const res = await fetch(BASE + path, {
    method,
    headers,
    body: body === null ? undefined : JSON.stringify(body ?? {}),
  });
  const text = await res.text();
  try {
    return JSON.parse(text);
  } catch {
    return { success: false, message: text.slice(0, 200) };
  }
}

async function upload(tok, releaseId, a) {
  const buf = readFileSync(a.file);
  const form = new FormData();
  form.append('releaseId', String(releaseId));
  form.append('platform', a.platform);
  form.append('arch', a.arch);
  form.append('format', a.format);
  if (a.minOs) form.append('minOs', String(a.minOs));
  form.append('file', new Blob([buf]), basename(a.file));
  const res = await fetch(`${BASE}/admin/artifacts`, {
    method: 'POST',
    headers: { Authorization: `Bearer ${tok}` },
    body: form,
  });
  const text = await res.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = { success: false, message: text.slice(0, 200) };
  }
  return { ...json, _sha256: createHash('sha256').update(buf).digest('hex'), _size: buf.length };
}

async function findApp(tok, appKey) {
  const apps = (await api('GET', '/admin/apps', null, tok)).data ?? [];
  const app = apps.find((x) => x.appKey === appKey);
  if (!app) throw new Error(`后台没有应用 ${appKey}，先建应用`);
  return app;
}

async function findRelease(tok, app, versionName) {
  // pageSize 上限 100；服务端会忽略 perPage 之类的未知查询参数。
  const list = (await api('GET', `/admin/releases?appId=${app.id}&pageSize=100`, null, tok)).data?.records ?? [];
  return list.find((r) => r.appId === app.id && r.versionName === versionName);
}

const STATUS_NAME = { 0: 'draft', 1: 'gray', 2: 'full', 3: 'offline' };

async function releaseDetail(tok, releaseId) {
  const res = await api('GET', `/admin/releases/${releaseId}`, null, tok);
  if (res.success !== true || !res.data) throw new Error('读取版本详情失败');
  return res.data;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const cmd = args._[0];

  if (cmd === 'apps') {
    const tok = await token(args);
    const res = await api('GET', '/admin/apps', null, tok);
    for (const a of res.data ?? []) console.log(`${a.id}\t${a.appKey}\t${a.name}`);
    return;
  }

  if (cmd === 'auth-check') {
    if (!hasCredentials(args)) {
      throw new Error('缺少凭据：设置 VAKAO_HUB_TOKEN，或 VAKAO_HUB_USER + VAKAO_HUB_PASS');
    }
    console.log('凭据检查通过');
    return;
  }

  if (cmd === 'release') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    const mode = resolvePublishMode(args);
    if (!args.artifacts.length) throw new Error('至少给一个 --file');
    for (const a of args.artifacts) {
      for (const k of ['platform', 'arch', 'format']) {
        if (!a[k]) throw new Error(`--file ${a.file} 缺少 --${k}`);
      }
    }
    const code = Number(args.code ?? versionCode(args.version));

    const tok = await token(args);
    const app = await findApp(tok, args.app);
    let release = await findRelease(tok, app, args.version);
    if (release) {
      console.log(`复用已有版本 ${args.version} (id=${release.id})`);
    } else {
      const res = await api(
        'POST',
        '/admin/releases',
        {
          appId: app.id,
          versionName: args.version,
          versionCode: code,
          channel: args.channel ?? 'stable',
          updateLog: args.notes ?? '',
        },
        tok,
      );
      if (res.success !== true) throw new Error('建版本失败：' + JSON.stringify(res).slice(0, 300));
      release = res.data;
      console.log(`新建版本 ${args.version} (id=${release.id}, code=${code})`);
    }

    // --replace 先删同平台/架构/格式的旧产物；失败重跑时避免 Conflict「产物已存在」。
    if (args.replace) {
      const detail = await releaseDetail(tok, release.id);
      for (const a of args.artifacts) {
        const existing = (detail.artifacts ?? []).find(
          (x) => x.platform === a.platform && x.arch === a.arch && x.format === a.format,
        );
        if (!existing) continue;
        const res = await api('DELETE', `/admin/artifacts/${existing.id}`, null, tok);
        if (res.success !== true) {
          throw new Error(`替换旧产物失败（id=${existing.id}）：${res.message ?? ''}`);
        }
        console.log(`  替换：已删除旧产物 ${a.platform}/${a.arch}/${a.format} (id=${existing.id})`);
      }
    }

    for (const a of args.artifacts) {
      const up = await upload(tok, release.id, a);
      if (up.success !== true) throw new Error(`上传失败 ${basename(a.file)}: ${up.message ?? ''}`);
      console.log(
        `  上传 ${basename(a.file)} → ${a.platform}/${a.arch}/${a.format} ` +
          `(${(up._size / 1024 / 1024).toFixed(1)} MB, sha256 ${up._sha256.slice(0, 12)}…)`,
      );
    }

    if (mode !== 'none') {
      const body =
        mode === 'gray' ? { mode: 'gray', grayPercent: Number(args['gray-percent'] ?? 10) } : { mode: 'full' };
      const res = await api('POST', `/admin/releases/${release.id}/publish`, body, tok);
      // 发布接口失败时也必须让整条发布链退非零，不能只打印响应。
      if (res.success !== true) {
        throw new Error(`发布失败（${mode}）：${res.message ?? JSON.stringify(res).slice(0, 200)}`);
      }
      console.log(`发布完成（${mode}）`);
    } else {
      console.log('已上传，未发布（--publish none）');
    }
    return;
  }

  if (cmd === 'status') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    const expect = args.expect ?? null;
    if (expect !== null && !Object.hasOwn(EXPECTED_STATUS, expect)) {
      throw new Error(`--expect 只接受 full/gray，收到: ${JSON.stringify(expect)}`);
    }
    const tok = await token(args);
    const app = await findApp(tok, args.app);
    const release = await findRelease(tok, app, args.version);
    if (!release) throw new Error(`后台找不到版本 ${args.version}`);

    const statusName = STATUS_NAME[release.status] ?? 'unknown';
    console.log(
      `版本 ${args.version}: id=${release.id}, code=${release.versionCode}, ` +
        `status=${statusName}, grayPercent=${release.grayPercent}`,
    );
    if (args.code !== undefined && Number(args.code) !== release.versionCode) {
      throw new Error(`versionCode 不一致：后台 ${release.versionCode}，期望 ${args.code}`);
    }
    if (expect && release.status !== EXPECTED_STATUS[expect]) {
      throw new Error(`发布复核失败：${args.version} 当前状态 ${statusName}，期望 ${expect}`);
    }
    return;
  }

  // stage 只保证“可以上传”：已全量/灰度的版本先显式下架，不靠人工记得。
  if (cmd === 'stage') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    const tok = await token(args);
    const app = await findApp(tok, args.app);
    const release = await findRelease(tok, app, args.version);
    if (!release) {
      console.log(`版本 ${args.version} 不存在，上传时会创建草稿`);
      return;
    }
    const statusName = STATUS_NAME[release.status] ?? 'unknown';
    if (release.status === 1 || release.status === 2) {
      const res = await api('POST', `/admin/releases/${release.id}/offline`, null, tok);
      if (res.success !== true) {
        throw new Error(`临时下架失败：${res.message ?? JSON.stringify(res).slice(0, 200)}`);
      }
      console.log(`已临时下架 ${args.version}（原状态 ${statusName}），用于补充产物；上传完成后统一发布`);
      return;
    }
    console.log(`版本 ${args.version} 已处于可上传状态（${statusName}）`);
    return;
  }

  if (cmd === 'publish') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    const mode = resolvePublishMode(args);
    if (mode === 'none') throw new Error('publish 不接受 --publish none；不发布请直接跳过该步骤');
    const tok = await token(args);
    const app = await findApp(tok, args.app);
    const release = await findRelease(tok, app, args.version);
    if (!release) throw new Error(`后台找不到版本 ${args.version}`);
    if (args.code !== undefined && Number(args.code) !== release.versionCode) {
      throw new Error(`versionCode 不一致：后台 ${release.versionCode}，期望 ${args.code}`);
    }
    const statusName = STATUS_NAME[release.status] ?? 'unknown';
    if (release.status !== 0 && release.status !== 3) {
      throw new Error(`版本 ${args.version} 当前为 ${statusName}，只有草稿/已下架版本可以发布`);
    }
    const body = mode === 'gray' ? { mode: 'gray', grayPercent: Number(args['gray-percent'] ?? 10) } : { mode: 'full' };
    const res = await api('POST', `/admin/releases/${release.id}/publish`, body, tok);
    if (res.success !== true) {
      throw new Error(`发布失败（${mode}）：${res.message ?? JSON.stringify(res).slice(0, 200)}`);
    }
    console.log(`发布完成：${args.version} → ${mode}`);
    return;
  }

  // verify 用于统一发布后的复核：状态、versionCode 与各平台产物必须同时满足。
  if (cmd === 'verify') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    if (!args.platforms) throw new Error('缺少 --platforms（例如 darwin,win32）');
    const expect = args.expect ?? 'full';
    if (!Object.hasOwn(EXPECTED_STATUS, expect)) {
      throw new Error(`--expect 只接受 full/gray，收到: ${JSON.stringify(expect)}`);
    }
    const platforms = String(args.platforms)
      .split(',')
      .map((x) => x.trim())
      .filter(Boolean);
    if (!platforms.length) throw new Error('--platforms 不能为空');

    const tok = await token(args);
    const app = await findApp(tok, args.app);
    const release = await findRelease(tok, app, args.version);
    if (!release) throw new Error(`后台找不到版本 ${args.version}`);
    const detail = await releaseDetail(tok, release.id);
    const statusName = STATUS_NAME[detail.status] ?? 'unknown';
    if (args.code !== undefined && Number(args.code) !== detail.versionCode) {
      throw new Error(`versionCode 不一致：后台 ${detail.versionCode}，期望 ${args.code}`);
    }
    if (detail.status !== EXPECTED_STATUS[expect]) {
      throw new Error(`发布复核失败：${args.version} 当前状态 ${statusName}，期望 ${expect}`);
    }
    const artifacts = detail.artifacts ?? [];
    for (const platform of platforms) {
      const matched = artifacts.filter((x) => x.platform === platform);
      if (!matched.length) throw new Error(`发布复核失败：${platform} 没有产物`);
      console.log(
        `  ${platform}: ` +
          matched.map((x) => `${x.arch}/${x.format} ${(x.size / 1024 / 1024).toFixed(1)}MB`).join(', '),
      );
    }
    console.log(`发布复核通过：${args.version} @ ${expect}，平台 ${platforms.join('/')}`);
    return;
  }

  console.log(
    '用法: node tools/hub-cli.mjs apps | auth-check | ' +
      'release --app <key> --version <v> --file <path> --platform <p> --arch <a> --format <f> ' +
      '[--publish none|gray|full] [--replace] | status --app <key> --version <v> [--expect full|gray] | ' +
      'stage --app <key> --version <v> | publish --app <key> --version <v> [--publish full|gray] | ' +
      'verify --app <key> --version <v> [--code <n>] [--expect full|gray] --platforms darwin,win32',
  );
}

const isMain = process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href;
if (isMain) {
  main().catch((e) => {
    console.error('✗', e.message);
    process.exit(1);
  });
}
