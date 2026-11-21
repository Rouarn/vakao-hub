import assert from 'node:assert/strict';
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';

import { parseArgs, resolvePublishMode } from '../../../tools/hub-cli.mjs';
import { parseInfo, versionCode } from '../../../scripts/version.mjs';

const root = fileURLToPath(new URL('../../..', import.meta.url));
const cli = join(root, 'tools', 'hub-cli.mjs');
const archiveChecker = join(root, 'scripts', 'check-archive.py');

/** mock 后台跑在本进程里时不能用 spawnSync，否则阻塞事件循环导致假死。 */
function runCliAsync(args, env) {
  return new Promise((resolve) => {
    const child = spawn(process.execPath, [cli, ...args], {
      cwd: root,
      env: { ...process.env, ...env },
    });
    let stderr = '';
    let stdout = '';
    child.stdout.on('data', (chunk) => (stdout += chunk));
    child.stderr.on('data', (chunk) => (stderr += chunk));
    child.on('close', (code) => resolve({ status: code, stdout, stderr }));
  });
}

/** 启动一次性 mock 后台，返回 base url。 */
async function mockHub(handler) {
  const server = createServer((req, res) => {
    const chunks = [];
    req.on('data', (chunk) => chunks.push(chunk));
    req.on('end', () => {
      const send = (body) => {
        res.writeHead(200, { 'content-type': 'application/json' });
        res.end(JSON.stringify(body));
      };
      // 客户端请求带 /api/v1 前缀，这里剥掉后按业务路径分发。
      const path = new URL(req.url, 'http://localhost').pathname.replace(/^\/api\/v1/, '');
      try {
        handler({ method: req.method, path }, send, Buffer.concat(chunks));
      } catch (error) {
        res.writeHead(500, { 'content-type': 'application/json' });
        res.end(JSON.stringify({ success: false, message: String(error) }));
      }
    });
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address();
  return {
    url: `http://127.0.0.1:${port}/api/v1`,
    close: () =>
      new Promise((resolve) => {
        server.closeAllConnections();
        server.close(resolve);
      }),
  };
}

function releaseFixture() {
  const dir = mkdtempSync(join(tmpdir(), 'vakao-release-'));
  const file = join(dir, 'Demo.zip');
  writeFileSync(file, 'fixture');
  return { dir, file };
}

test('versionCode 单调递增且避开旧的两位数撞号', () => {
  assert.equal(versionCode('1.1.0'), 1_001_000);
  assert.ok(versionCode('1.1.0') > 110, '已发布 1.1.0 的旧 code 是 110');
  assert.ok(versionCode('1.1.1') > versionCode('1.1.0'));
  assert.ok(versionCode('1.2.0') > versionCode('1.1.9'));
  assert.ok(versionCode('2.0.0') > versionCode('1.99.99'));
  assert.notEqual(versionCode('1.11.0'), versionCode('1.1.10'));
  assert.throws(() => versionCode('1.1000.0'), /不能超过 999/);
});

test('config.yml 解析不依赖固定两格缩进', () => {
  const fourSpaces = 'info:\n    version: "2.3.4"\n    productName: Demo\nother:\n  version: 9\n';
  assert.equal(parseInfo(fourSpaces).version, '2.3.4');
  assert.equal(parseInfo(fourSpaces).productName, 'Demo');
  assert.equal(parseInfo('info:\n  version: 1.2.3 # 注释\n').version, '1.2.3');
});

test('--publish 只接受 none/gray/full，等号写法与未知值报错', () => {
  assert.equal(resolvePublishMode(parseArgs([])), 'full');
  assert.equal(resolvePublishMode(parseArgs(['--publish', 'gray'])), 'gray');
  assert.equal(resolvePublishMode(parseArgs(['--publish', 'none'])), 'none');
  assert.throws(() => resolvePublishMode(parseArgs(['--publish', 'ful'])), /只接受/);
  assert.throws(() => resolvePublishMode(parseArgs(['--publish'])), /只接受/);
  assert.throws(() => parseArgs(['--publish=full']), /等号写法/);
});

test('publish 返回 success=false 时 CLI 退非零', async () => {
  const { dir, file } = releaseFixture();
  const hub = await mockHub((req, send) => {
    if (req.path === '/admin/apps') return send({ success: true, data: [{ id: 1, appKey: 'demo', name: 'Demo' }] });
    if (req.path === '/admin/releases' && req.method === 'GET') {
      return send({ success: true, data: { records: [] } });
    }
    if (req.path === '/admin/releases' && req.method === 'POST') return send({ success: true, data: { id: 9 } });
    if (req.path === '/admin/artifacts') return send({ success: true, data: {} });
    if (req.path === '/admin/releases/9/publish') return send({ success: false, message: '权限不足' });
    send({ success: false, message: `意外请求 ${req.method} ${req.path}` });
  });

  try {
    const result = await new Promise((resolve) => {
      const child = spawn(
        process.execPath,
        [
          cli,
          'release',
          '--app',
          'demo',
          '--version',
          '1.0.0',
          '--file',
          file,
          '--platform',
          'darwin',
          '--arch',
          'arm64',
          '--format',
          'zip',
          '--publish',
          'full',
        ],
        { cwd: root, env: { ...process.env, VAKAO_HUB_URL: hub.url, VAKAO_HUB_TOKEN: 'test-token' } },
      );
      let stderr = '';
      child.stderr.on('data', (chunk) => (stderr += chunk));
      child.on('close', (code) => resolve({ code, stderr }));
    });
    assert.equal(result.code, 1);
    assert.match(result.stderr, /发布失败/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
    await hub.close();
  }
});

test('发布后复核发现状态不符时退非零', async () => {
  const hub = await mockHub((req, send) => {
    if (req.path === '/admin/apps') return send({ success: true, data: [{ id: 1, appKey: 'demo', name: 'Demo' }] });
    if (req.path === '/admin/releases') {
      return send({
        success: true,
        data: {
          records: [{ id: 9, appId: 1, versionName: '1.0.0', versionCode: 1_000_000, status: 0, grayPercent: 0 }],
        },
      });
    }
    send({ success: false, message: `意外请求 ${req.method} ${req.path}` });
  });

  try {
    const result = await runCliAsync(
      ['status', '--app', 'demo', '--version', '1.0.0', '--code', '1000000', '--expect', 'full'],
      { VAKAO_HUB_URL: hub.url, VAKAO_HUB_TOKEN: 'test-token' },
    );
    assert.equal(result.status, 1);
    assert.match(result.stderr, /发布复核失败/);
  } finally {
    await hub.close();
  }
});

function makeApp(base, version, binary) {
  const app = join(base, 'VaKaoHub.app');
  mkdirSync(join(app, 'Contents', 'MacOS'), { recursive: true });
  const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleShortVersionString</key><string>${version}</string>
<key>CFBundleIdentifier</key><string>com.wails.vakaohub</string>
<key>CFBundleExecutable</key><string>VaKaoHub</string>
</dict></plist>
`;
  writeFileSync(join(app, 'Contents', 'Info.plist'), plist);
  const bin = join(app, 'Contents', 'MacOS', 'VaKaoHub');
  writeFileSync(bin, binary);
  chmodSync(bin, 0o755);
  return app;
}

function makeZip(base, app, name = 'VaKaoHub.zip') {
  const zip = join(base, name);
  const result = spawnSync('zip', ['-qry', zip, app.split('/').pop()], { cwd: base, encoding: 'utf8' });
  assert.equal(result.status, 0, result.stderr);
  return zip;
}

function checkArchive(zip, version, app) {
  return spawnSync(
    'python3',
    [archiveChecker, '--archive', zip, '--version', version, '--product', 'VaKaoHub', '--app', app],
    { encoding: 'utf8' },
  );
}

test('归档结构检查：缺可执行文件、顶层多余条目都拒绝', () => {
  const base = mkdtempSync(join(tmpdir(), 'vakao-archive-'));
  try {
    const app = makeApp(base, '1.1.0', 'binary-v1');
    const zip = makeZip(base, app);
    const ok = checkArchive(zip, '1.1.0', app);
    assert.equal(ok.status, 0, ok.stderr);

    writeFileSync(join(base, 'stray.txt'), 'x');
    const strayZip = join(base, 'stray.zip');
    const strayResult = spawnSync('zip', ['-qry', strayZip, 'VaKaoHub.app', 'stray.txt'], { cwd: base });
    assert.equal(strayResult.status, 0, strayResult.stderr);
    const stray = checkArchive(strayZip, '1.1.0', app);
    assert.notEqual(stray.status, 0);
    assert.match(stray.stderr, /顶层/);

    rmSync(join(app, 'Contents', 'MacOS', 'VaKaoHub'));
    const broken = makeZip(base, app, 'broken.zip');
    const missing = checkArchive(broken, '1.1.0', app);
    assert.notEqual(missing.status, 0);
    assert.match(missing.stderr, /可执行文件/);
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

test('--no-build 复用归档时，与当前 .app 不一致必须拒绝', () => {
  const base = mkdtempSync(join(tmpdir(), 'vakao-stale-'));
  try {
    const oldApp = makeApp(join(base, 'old'), '1.1.0', 'binary-old');
    const zip = makeZip(join(base, 'old'), oldApp);
    const currentApp = makeApp(join(base, 'current'), '1.1.0', 'binary-current');
    const result = checkArchive(zip, '1.1.0', currentApp);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /不一致/);
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});

test('归档内 Info.plist 版本与 VERSION 不一致时拒绝', () => {
  const base = mkdtempSync(join(tmpdir(), 'vakao-version-'));
  try {
    const app = makeApp(base, '1.0.0', 'binary');
    const zip = makeZip(base, app);
    const result = checkArchive(zip, '1.1.0', app);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /版本/);
  } finally {
    rmSync(base, { recursive: true, force: true });
  }
});
