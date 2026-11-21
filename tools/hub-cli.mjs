#!/usr/bin/env node
/**
 * vakao-hub 发布 CLI：构建完一条命令把产物推到软件中心。
 *
 * 用法：
 *   node tools/hub-cli.mjs apps
 *   node tools/hub-cli.mjs release --app dockdeck --version 1.0.0 --code 100 \
 *     --file ./DockDeck.zip --platform darwin --arch arm64 --format zip --min-os 12.0 \
 *     --notes "1.0.0 首发" [--publish full] [--no-publish]
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

const BASE = process.env.VAKAO_HUB_URL || 'https://dist.woyuxiaji.cn/api/v1';
const CACHE = join(homedir(), '.cache', 'vakao-hub', 'token.json');

function parseArgs(argv) {
  const out = { _: [], artifacts: [] };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (!a.startsWith('--')) {
      out._.push(a);
      continue;
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

async function token(args) {
  if (args['token-file']) return readFileSync(args['token-file'], 'utf8').trim();
  if (process.env.VAKAO_HUB_TOKEN) return process.env.VAKAO_HUB_TOKEN.trim();
  if (existsSync(CACHE)) {
    const cached = JSON.parse(readFileSync(CACHE, 'utf8'));
    if (cached.expiresAt && Date.now() < cached.expiresAt) return cached.token;
  }
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

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const cmd = args._[0];
  const tok = await token(args);

  if (cmd === 'apps') {
    const res = await api('GET', '/admin/apps', null, tok);
    for (const a of res.data ?? []) console.log(`${a.id}\t${a.appKey}\t${a.name}`);
    return;
  }

  if (cmd === 'release') {
    if (!args.app || !args.version) throw new Error('缺少 --app 或 --version');
    const code = Number(args.code ?? args.version.replace(/\./g, ''));
    const apps = (await api('GET', '/admin/apps', null, tok)).data ?? [];
    const app = apps.find((x) => x.appKey === args.app);
    if (!app) throw new Error(`后台没有应用 ${args.app}，先建应用`);

    const list = (await api('GET', '/admin/releases?perPage=100', null, tok)).data?.records ?? [];
    let release = list.find((r) => r.appId === app.id && r.versionName === args.version);
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
      if (!res.success) throw new Error('建版本失败：' + JSON.stringify(res).slice(0, 300));
      release = res.data;
      console.log(`新建版本 ${args.version} (id=${release.id}, code=${code})`);
    }

    if (!args.artifacts.length) throw new Error('至少给一个 --file');

    if (args['dry-run']) {
      console.log('[dry-run] 应用:', app.appKey, '| 版本:', args.version, '| code:', code);
      console.log('[dry-run] 版本动作:', release ? `复用 #${release.id}` : '新建');
      for (const a of args.artifacts) {
        console.log(`[dry-run] 上传 ${basename(a.file)} → ${a.platform}/${a.arch}/${a.format}`);
      }
      console.log('[dry-run] 发布模式:', args.publish ?? 'full');
      return;
    }
    for (const a of args.artifacts) {
      for (const k of ['platform', 'arch', 'format']) {
        if (!a[k]) throw new Error(`--file ${a.file} 缺少 --${k}`);
      }
      const up = await upload(tok, release.id, a);
      if (!up.success) throw new Error(`上传失败 ${basename(a.file)}: ${up.message ?? ''}`);
      console.log(
        `  上传 ${basename(a.file)} → ${a.platform}/${a.arch}/${a.format} ` +
          `(${(up._size / 1024 / 1024).toFixed(1)} MB, sha256 ${up._sha256.slice(0, 12)}…)`,
      );
    }

    const mode = args.publish ?? 'full';
    if (mode !== 'none') {
      const body =
        mode === 'gray' ? { mode: 'gray', grayPercent: Number(args['gray-percent'] ?? 10) } : { mode: 'full' };
      const res = await api('POST', `/admin/releases/${release.id}/publish`, body, tok);
      console.log('发布:', res.success ? `ok (${mode})` : JSON.stringify(res).slice(0, 200));
    } else {
      console.log('已上传，未发布（--no-publish）');
    }
    return;
  }

  console.log(
    '用法: node tools/hub-cli.mjs apps | release --app <key> --version <v> --file <path> --platform <p> --arch <a> --format <f> [--publish full|gray|none]',
  );
}

main().catch((e) => {
  console.error('✗', e.message);
  process.exit(1);
});
