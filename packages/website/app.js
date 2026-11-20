(function () {
  'use strict';

  var params = new URLSearchParams(location.search);
  var PLATFORMS = ['darwin', 'win32', 'android'];
  var LABELS = {
    darwin: 'macOS',
    win32: 'Windows',
    android: 'Android',
    linux: 'Linux',
  };
  var TABS = [
    ['darwin', 'macOS'],
    ['win32', 'Windows'],
    ['android', 'Android'],
    ['all', '全部'],
  ];
  var STORE_PLATFORM = 'vakao_hub_platform';
  var STORE_FILTER = 'vakao_hub_filter';
  var STORE_THEME = 'vakao_hub_theme';
  var palette = ['#5fbedc', '#f0995f', '#a27fc2', '#eddf55'];

  function readStore(key) {
    try {
      return localStorage.getItem(key);
    } catch (error) {
      return null;
    }
  }

  function writeStore(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch (error) {
      /* 隐私模式下忽略 */
    }
  }

  function detectPlatform() {
    var ua = navigator.userAgent.toLowerCase();
    if (/android/.test(ua)) return 'android';
    if (/iphone|ipad|ipod|mac/.test(ua)) return 'darwin';
    if (/win/.test(ua)) return 'win32';
    return 'linux';
  }

  function resolveApi() {
    var override = params.get('api');
    if (override) return override.replace(/\/+$/, '');
    if (location.hostname === 'localhost' || location.hostname === '127.0.0.1') {
      if (location.port && location.port !== '3000') {
        return 'http://127.0.0.1:3000/api/v1';
      }
    }
    if (location.protocol === 'file:') return 'http://127.0.0.1:3000/api/v1';
    return '/api/v1';
  }

  var savedPlatform = readStore(STORE_PLATFORM);
  var savedFilter = readStore(STORE_FILTER);
  var initialPlatform = LABELS[savedPlatform] ? savedPlatform : detectPlatform();
  var selectableFilter =
    savedFilter === 'all' || savedFilter === 'darwin' || savedFilter === 'win32' || savedFilter === 'android';
  var initialFilter = selectableFilter
    ? savedFilter
    : PLATFORMS.indexOf(initialPlatform) >= 0
      ? initialPlatform
      : 'all';

  var state = {
    platform: initialPlatform,
    filter: initialFilter,
    apps: [],
    loading: true,
    error: '',
  };

  var apiBase = resolveApi();
  var grid = document.getElementById('grid');
  var tabsBox = document.getElementById('tabs');
  var heroCta = document.getElementById('hero-cta');
  var navCta = document.getElementById('nav-cta');
  var kicker = document.getElementById('kicker');
  var specOs = document.getElementById('spec-os');
  var featBox = document.getElementById('launch-feat');
  var apiLabel = document.getElementById('api-label');

  function esc(value) {
    return String(value === null || value === undefined ? '' : value).replace(
      /[&<>"']/g,
      function (char) {
        return {
          '&': '&amp;',
          '<': '&lt;',
          '>': '&gt;',
          '"': '&quot;',
          "'": '&#39;',
        }[char];
      },
    );
  }

  function colorFor(key) {
    var hash = 0;
    for (var index = 0; index < key.length; index += 1) {
      hash = (hash * 31 + key.charCodeAt(index)) >>> 0;
    }
    return palette[hash % palette.length];
  }

  // 与 @vakao/hub-shared 的 formatBytes 保持同一口径（website 无构建，按共享规则实现）。
  function formatBytes(bytes) {
    if (bytes === null || bytes === undefined || !isFinite(bytes) || bytes < 0)
      return '未知大小';
    if (!bytes) return '0 B';
    var units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var index = Math.min(
      Math.floor(Math.log(bytes) / Math.log(1024)),
      units.length - 1,
    );
    return (bytes / Math.pow(1024, index)).toFixed(1) + ' ' + units[index];
  }

  function trimOs(value) {
    return String(value || '').replace(/\.0$/, '');
  }

  function osText(platform, minOs) {
    if (platform === 'darwin') return 'macOS ' + trimOs(minOs) + '+';
    if (platform === 'win32') return 'Windows ' + trimOs(minOs) + '+';
    if (platform === 'android') return 'Android';
    return 'Desktop';
  }

  function archLabel(platform, arch) {
    if (!arch || arch === 'universal') return 'Universal';
    if (arch === 'arm64') return platform === 'darwin' ? 'Apple Silicon' : 'arm64';
    if (arch === 'x64') return platform === 'darwin' ? 'Intel' : 'x64';
    return arch;
  }

  // 匿名设备标识：只用于事件去重与漏斗统计，存在 localStorage，不采集任何硬件信息。
  var DEVICE_KEY = 'vakao_hub_device';
  var reportedDownloads = {};

  function deviceID() {
    try {
      var value = localStorage.getItem(DEVICE_KEY);
      if (!value) {
        value =
          'web-' +
          Date.now().toString(36) +
          '-' +
          Math.random().toString(36).slice(2, 10);
        localStorage.setItem(DEVICE_KEY, value);
      }
      return value;
    } catch (error) {
      return '';
    }
  }

  // 下载点击上报：失败静默丢弃，绝不阻塞或改变下载行为；同一产物本次会话只报一次。
  function reportDownloadStart(app, artifact) {
    if (!app || !artifact || !artifact.id) return;
    var release = app.releases[artifact.platform];
    if (!release) return;
    var key = String(artifact.id);
    if (reportedDownloads[key]) return;
    reportedDownloads[key] = true;

    var payload = {
      appKey: app.appKey,
      platform: artifact.platform,
      arch: artifact.arch,
      type: 'download_start',
      releaseId: release.releaseId,
      artifactId: artifact.id,
      toVersionCode: release.versionCode,
    };
    var device = deviceID();
    if (device) payload.deviceId = device;

    try {
      fetch(apiBase + '/updates/report', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
        keepalive: true,
      }).catch(function () {
        /* 上报失败不影响下载 */
      });
    } catch (error) {
      /* 上报失败不影响下载 */
    }
  }

  function findApp(appKey) {
    return (
      state.apps.filter(function (app) {
        return app.appKey === appKey;
      })[0] || null
    );
  }

  function findArtifact(app, artifactId) {
    if (!app) return null;
    var hit = null;
    Object.keys(app.artifacts).forEach(function (platform) {
      app.artifacts[platform].forEach(function (artifact) {
        if (artifact.id === artifactId) hit = artifact;
      });
    });
    return hit;
  }

  function platformCount(platform) {
    return state.apps.filter(function (app) {
      return (app.artifacts[platform] || []).length > 0;
    }).length;
  }

  function versionFor(app) {
    if (state.filter !== 'all') {
      var release = app.releases[state.filter];
      if (release) return release.versionName;
    }
    var best = null;
    Object.keys(app.releases).forEach(function (platform) {
      var current = app.releases[platform];
      if (!best || current.versionCode > best.versionCode) best = current;
    });
    return best ? best.versionName : '';
  }

  function iconFor(app) {
    if (app.iconUrl) {
      return '<img src="' + esc(app.iconUrl) + '" alt="" loading="lazy" />';
    }
    var initial = (app.name || '?').trim().charAt(0).toUpperCase();
    return (
      '<span class="ico" style="background:' +
      esc(colorFor(app.appKey)) +
      '">' +
      esc(initial) +
      '</span>'
    );
  }

  function renderTabs() {
    var loaded = !state.loading && !state.error && state.apps.length > 0;
    var counts = {
      all: state.apps.length,
      darwin: platformCount('darwin'),
      win32: platformCount('win32'),
      android: platformCount('android'),
    };
    tabsBox.innerHTML = TABS.map(function (tab) {
      var key = tab[0];
      return (
        '<button class="tab" role="tab" data-p="' +
        key +
        '" aria-selected="' +
        String(state.filter === key) +
        '">' +
        esc(tab[1]) +
        (loaded ? '<b>' + counts[key] + '</b>' : '') +
        '</button>'
      );
    }).join('');

    Array.prototype.forEach.call(tabsBox.querySelectorAll('.tab'), function (el) {
      el.addEventListener('click', function () {
        var key = el.dataset.p;
        state.filter = key;
        /* 只有点具体平台时才改变下载入口的平台；「全部」只影响列表 */
        if (key !== 'all') {
          state.platform = key;
          writeStore(STORE_PLATFORM, key);
        }
        writeStore(STORE_FILTER, key);
        render();
      });
    });
  }

  function downloadLinks(app) {
    var platform = state.platform;
    var arts = app.artifacts[platform] || [];
    if (!arts.length) {
      return (
        '<span class="dl" aria-disabled="true">暂无 ' + esc(LABELS[platform]) + ' 版</span>'
      );
    }
    var prefix = state.filter === 'all' ? LABELS[platform] + ' ' : '';
    return arts
      .map(function (artifact) {
        var arch = arts.length > 1 ? artifact.arch + ' ' : '';
        return (
          '<a class="dl" href="' +
          esc(artifact.downloadUrl) +
          '" rel="noreferrer" data-app="' +
          esc(app.appKey) +
          '" data-artifact="' +
          artifact.id +
          '">↓ ' +
          esc(prefix + '.' + artifact.format) +
          ' ' +
          esc(arch) +
          '<b>' +
          formatBytes(artifact.size) +
          '</b></a>'
        );
      })
      .join('');
  }

  function renderGrid() {
    if (state.loading) {
      grid.innerHTML = '<div class="empty">正在读取软件目录…</div>';
      return;
    }
    if (state.error) {
      grid.innerHTML = '<div class="empty">' + esc(state.error) + '</div>';
      return;
    }

    var visible =
      state.filter === 'all'
        ? state.apps
        : state.apps.filter(function (app) {
            return (app.artifacts[state.filter] || []).length > 0;
          });

    if (!visible.length) {
      grid.innerHTML = '<div class="empty">该平台暂无发布版本。</div>';
      return;
    }

    grid.innerHTML = visible
      .map(function (app) {
        var version = versionFor(app);
        var meta = esc(app.description || '暂无描述');
        if (version) meta += ' · v' + esc(version);
        if (app.homepage) {
          meta +=
            ' · <a class="home" href="' +
            esc(app.homepage) +
            '" target="_blank" rel="noreferrer">主页<i>↗</i></a>';
        }
        return (
          '<div class="item">' +
          iconFor(app) +
          '<div><h3>' +
          esc(app.name) +
          (app.appKey === 'vakaohub' ? '<em>启动器</em>' : '') +
          '</h3><p>' +
          meta +
          '</p></div>' +
          '<div class="dls">' +
          downloadLinks(app) +
          '</div></div>'
        );
      })
      .join('');
  }

  function renderHero() {
    if (state.loading) {
      kicker.textContent = 'VaKao Hub · SOFTWARE SUITE';
      specOs.textContent = '正在读取目录';
      heroCta.innerHTML = '<span class="btn" aria-disabled="true">读取目录中…</span>';
      navCta.innerHTML = '<span class="mini" aria-disabled="true">读取中…</span>';
      return;
    }

    if (state.error) {
      kicker.textContent = 'VaKao Hub · SOFTWARE SUITE';
      specOs.textContent = '版本信息暂不可用';
      heroCta.innerHTML = '<span class="btn" aria-disabled="true">目录暂不可用</span>';
      navCta.innerHTML = '<span class="mini" aria-disabled="true">下载启动器</span>';
      return;
    }

    var launcher = findApp('vakaohub');
    var platform = state.platform;
    if (!launcher) {
      kicker.textContent = 'VaKao Hub · SOFTWARE SUITE';
      specOs.textContent = '版本信息暂不可用';
      heroCta.innerHTML = '<span class="btn" aria-disabled="true">暂无可用版本</span>';
      navCta.innerHTML = '<span class="mini" aria-disabled="true">下载启动器</span>';
      return;
    }

    var arts = launcher.artifacts[platform] || [];
    var artifact = arts[0];
    var release = launcher.releases[platform];
    var desktopRelease =
      launcher.releases.darwin || launcher.releases.win32 || null;

    if (artifact && release) {
      kicker.textContent =
        osText(platform, artifact.minOs) + ' · v' + release.versionName;
      specOs.textContent =
        archLabel(platform, artifact.arch) + ' · ' + artifact.format.toUpperCase();
      heroCta.innerHTML =
        '<a class="btn btn-gold" href="' +
        esc(artifact.downloadUrl) +
        '" data-app="' +
        esc(launcher.appKey) +
        '" data-artifact="' +
        artifact.id +
        '">下载 for ' +
        esc(LABELS[platform]) +
        ' <span class="m">' +
        formatBytes(artifact.size) +
        '</span></a>';
      navCta.innerHTML =
        '<a class="mini" href="' +
        esc(artifact.downloadUrl) +
        '" data-app="' +
        esc(launcher.appKey) +
        '" data-artifact="' +
        artifact.id +
        '">下载启动器</a>';
      return;
    }

    /* Android / Linux 没有启动器产物：置灰，不放假链接 */
    var desktopVersion = desktopRelease ? ' · v' + desktopRelease.versionName : '';
    kicker.textContent = 'macOS / Windows' + desktopVersion;
    specOs.textContent = '仅桌面端 · 无 ' + LABELS[platform] + ' 包';
    heroCta.innerHTML =
      '<span class="btn" aria-disabled="true">启动器暂无 ' + esc(LABELS[platform]) + ' 版</span>';
    navCta.innerHTML =
      '<span class="mini" aria-disabled="true">暂无 ' + esc(LABELS[platform]) + ' 版</span>';
  }

  function render() {
    renderTabs();
    renderGrid();
    renderHero();
  }

  var FEATURES = [
    { t: '一个目录，所有版本', d: 'Android 与 macOS 共用一套应用、版本与产物模型。' },
    { t: '发布状态可追溯', d: '灰度、全量、强更与回滚都在后台留痕。' },
    { t: '安装前后校验', d: 'SHA-256 与 macOS codesign 完整性检查。' },
    { t: '安静的更新', d: '启动即检查，不弹窗轰炸，也不静默替换正在使用的版本。' },
  ];

  featBox.innerHTML = FEATURES.map(function (feature) {
    return '<div><h3>' + esc(feature.t) + '</h3><p>' + esc(feature.d) + '</p></div>';
  }).join('');

  /* ---------- 数据加载：三平台并行，任一失败按该平台无数据处理 ---------- */
  var lastError = '';

  function fetchPlatform(platform) {
    var url =
      apiBase +
      '/catalog?platform=' +
      encodeURIComponent(platform) +
      '&arch=universal';
    return fetch(url)
      .then(function (response) {
        return response.json().then(function (body) {
          if (!response.ok || body.code !== 200) {
            throw new Error(body && body.message ? body.message : 'HTTP ' + response.status);
          }
          return { platform: platform, apps: (body.data && body.data.apps) || [] };
        });
      })
      .catch(function (error) {
        lastError = error && error.message ? error.message : '网络异常';
        return null;
      });
  }

  function merge(results) {
    var map = {};
    results.forEach(function (result) {
      if (!result) return;
      result.apps.forEach(function (app) {
        var entry = map[app.appKey];
        if (!entry) {
          entry = map[app.appKey] = {
            appKey: app.appKey,
            name: app.name,
            description: app.description,
            iconUrl: app.iconUrl,
            homepage: app.homepage,
            artifacts: {},
            releases: {},
          };
        }
        var latest = app.latest;
        if (!latest) return;
        entry.releases[result.platform] = {
          releaseId: latest.id,
          versionCode: latest.versionCode,
          versionName: latest.versionName,
        };
        entry.artifacts[result.platform] = (latest.artifacts || []).map(function (artifact) {
          return {
            id: artifact.id,
            platform: artifact.platform,
            arch: artifact.arch,
            format: artifact.format,
            size: artifact.size,
            minOs: artifact.minOs,
            downloadUrl: artifact.downloadUrl,
          };
        });
      });
    });

    return Object.keys(map)
      .map(function (key) {
        return map[key];
      })
      .sort(function (a, b) {
        if (a.appKey === 'vakaohub') return -1;
        if (b.appKey === 'vakaohub') return 1;
        return a.appKey < b.appKey ? -1 : a.appKey > b.appKey ? 1 : 0;
      });
  }

  function load() {
    state.loading = true;
    state.error = '';
    lastError = '';
    render();

    Promise.all(PLATFORMS.map(fetchPlatform))
      .then(function (results) {
        var ok = results.filter(Boolean);
        if (!ok.length) throw new Error(lastError || '网络异常');
        state.apps = merge(results);
      })
      .catch(function (error) {
        state.apps = [];
        state.error = '加载失败：' + (error && error.message ? error.message : '网络异常');
      })
      .then(function () {
        state.loading = false;
        render();
      });
  }

  /* ---------- 下载点击上报（事件委托，首屏/顶栏/列表共用） ---------- */
  document.addEventListener('click', function (event) {
    var node = event.target;
    while (node && node !== document) {
      if (node.tagName === 'A' && node.getAttribute('data-app')) break;
      node = node.parentNode;
    }
    if (!node || node === document) return;
    var app = findApp(node.getAttribute('data-app'));
    var artifact = findArtifact(app, Number(node.getAttribute('data-artifact')));
    if (app && artifact) reportDownloadStart(app, artifact);
  });

  /* ---------- 深浅色 ---------- */
  var themeBtn = document.getElementById('theme-btn');

  function syncThemeBtn() {
    var isLight = document.documentElement.dataset.theme === 'light';
    themeBtn.setAttribute('aria-label', isLight ? '切换到暗色' : '切换到亮色');
    themeBtn.title = isLight ? '切换到暗色' : '切换到亮色';
  }

  themeBtn.addEventListener('click', function () {
    var next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
    document.documentElement.dataset.theme = next;
    writeStore(STORE_THEME, next);
    syncThemeBtn();
  });

  var mq = window.matchMedia('(prefers-color-scheme: light)');
  var onSchemeChange = function (event) {
    if (readStore(STORE_THEME)) return;
    document.documentElement.dataset.theme = event.matches ? 'light' : 'dark';
    syncThemeBtn();
  };
  if (mq.addEventListener) mq.addEventListener('change', onSchemeChange);
  else if (mq.addListener) mq.addListener(onSchemeChange);

  syncThemeBtn();
  if (apiLabel) apiLabel.textContent = apiBase;
  render();
  load();
})();
