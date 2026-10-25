// VidHawk service worker: observes network traffic (layer 1), receives page
// hook reports (layers 2/3), keeps a per-tab media registry and talks to the
// native engine. No code runs in the page from here; see content.js/inject.js.
importScripts('lib/classify.js', 'lib/naming.js');

const HOST = 'com.vidhawk.host';
const HEADER_KEEP = new Set(['referer', 'origin', 'cookie', 'user-agent', 'authorization', 'accept', 'accept-language', 'x-requested-with', 'sec-fetch-dest', 'sec-fetch-mode', 'sec-fetch-site']);
const TAKEOVER_EXTS = new Set([...VidHawkClassify.VIDEO, ...VidHawkClassify.AUDIO, ...VidHawkClassify.ARCHIVE, 'm3u8', 'mpd', 'torrent']);
const MAX_ITEMS = 80;
const STREAM_KINDS = new Set(['hls', 'dash']);

const state = {
  tabs: {},
  jobs: {},
  engine: { connected: false, info: null, lastPing: 0, error: null },
  settings: { takeOver: true, notifications: true },
};
const reqHeaders = new Map();
const urlHeaders = new Map();
const notificationPaths = new Map();

// ---------------------------------------------------------------- persistence
let saveTimer = null;
function persist() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => chrome.storage.session.set({ tabs: state.tabs, jobs: state.jobs, recentDownloads: state.recentDownloads || [] }).catch(() => {}), 250);
}
async function restore() {
  try {
    const s = await chrome.storage.session.get(['tabs', 'jobs', 'recentDownloads']);
    if (s.tabs) state.tabs = s.tabs;
    if (s.jobs) state.jobs = s.jobs;
    if (s.recentDownloads) state.recentDownloads = s.recentDownloads;
    const l = await chrome.storage.local.get('settings');
    if (l.settings) Object.assign(state.settings, l.settings);
  } catch {}
}
const ready = restore();

// ------------------------------------------------------------------ tab state
function stripHash(url) {
  try { const u = new URL(url); u.hash = ''; return u.href; } catch { return url || ''; }
}
function tabState(tabId) {
  return (state.tabs[tabId] ||= { url: '', meta: {}, items: {}, segments: null, updated: Date.now() });
}
function resetTab(tabId, url) {
  state.tabs[tabId] = { url: stripHash(url), meta: {}, items: {}, segments: null, updated: Date.now() };
  updateBadge(tabId);
  persist();
  notifyPopup();
}
function visibleItems(tab) {
  const items = Object.values(tab.items || {}).filter((i) => i.kind !== 'segment');
  const keys = new Set(items.map((i) => i.id));
  return items.filter((i) => !(i.childOf && keys.has(i.childOf)));
}
function updateBadge(tabId) {
  const tab = state.tabs[tabId];
  const n = tab ? visibleItems(tab).filter((i) => !i.stale).length : 0;
  chrome.action.setBadgeText({ tabId, text: n ? String(n) : '' }).catch(() => {});
}
chrome.action.setBadgeBackgroundColor({ color: '#f59e0b' }).catch(() => {});
chrome.action.setBadgeTextColor?.({ color: '#1c1917' }).catch(() => {});

function rememberHeaders(url, headers) {
  if (!headers || !Object.keys(headers).length) return;
  if (urlHeaders.size > 600) urlHeaders.delete(urlHeaders.keys().next().value);
  urlHeaders.set(stripHash(url), headers);
}
function headersFor(url, pageUrl) {
  return urlHeaders.get(stripHash(url)) || (pageUrl ? { referer: pageUrl } : {});
}

function addItem(tabId, item) {
  const tab = tabState(tabId);
  const key = stripHash(item.url);
  const existing = tab.items[key];
  if (existing) {
    existing.seen = (existing.seen || 1) + 1;
    existing.stale = false;
    if (item.headers && Object.keys(item.headers).length) existing.headers = item.headers;
    if (item.size && !existing.size) existing.size = item.size;
    if (item.filename && !existing.filename) existing.filename = item.filename;
    if (item.mime && !existing.mime) existing.mime = item.mime;
    if (item.verified) existing.verified = true;
    if (item.isMaster != null) existing.isMaster = item.isMaster;
    if (item.kind && existing.kind !== item.kind && item.verified) existing.kind = item.kind;
  } else {
    const keys = Object.keys(tab.items);
    if (keys.length >= MAX_ITEMS) {
      const oldest = keys.map((k) => tab.items[k]).sort((a, b) => a.added - b.added)[0];
      delete tab.items[oldest.id];
    }
    tab.items[key] = { id: key, seen: 1, added: Date.now(), stale: false, verified: true, ...item, url: item.url };
  }
  tab.updated = Date.now();
  updateBadge(tabId);
  persist();
  notifyPopup();
  return tab.items[key];
}
function markChildren(tabId, parentUrl, children) {
  const tab = tabState(tabId);
  const parentKey = stripHash(parentUrl);
  for (const child of children || []) {
    const k = stripHash(child);
    if (k === parentKey) continue;
    const it = tab.items[k];
    if (it) it.childOf = parentKey;
    else tab.items[k] = { id: k, url: child, kind: 'hls', ext: 'm3u8', childOf: parentKey, seen: 0, added: Date.now(), hidden: true, headers: tab.items[parentKey]?.headers || {} };
  }
  updateBadge(tabId);
  persist();
}
function noteSegment(tabId, url, res, headers) {
  const tab = tabState(tabId);
  const seg = (tab.segments ||= { count: 0, ext: res.ext, lastUrl: url, headers: {} });
  seg.count++;
  seg.ext = res.ext || seg.ext;
  seg.lastUrl = url;
  if (headers && Object.keys(headers).length) seg.headers = headers;
  tab.updated = Date.now();
  persist();
}

// ---------------------------------------------------------- layer 1: network
chrome.webRequest.onSendHeaders.addListener((d) => {
  const h = {};
  for (const x of d.requestHeaders || []) {
    const n = x.name.toLowerCase();
    if (HEADER_KEEP.has(n) && x.value) h[n] = x.value;
  }
  if (reqHeaders.size > 3000) reqHeaders.delete(reqHeaders.keys().next().value);
  reqHeaders.set(d.requestId, h);
}, { urls: ['<all_urls>'] }, ['requestHeaders', 'extraHeaders']);

chrome.webRequest.onHeadersReceived.addListener((d) => {
  const headers = reqHeaders.get(d.requestId) || {};
  reqHeaders.delete(d.requestId);
  const resp = {};
  for (const x of d.responseHeaders || []) resp[x.name.toLowerCase()] = x.value;
  const res = VidHawkClassify.classify({
    url: d.url, resourceType: d.type, statusCode: d.statusCode, mime: resp['content-type'],
    contentLength: resp['content-length'], contentDisposition: resp['content-disposition'], contentRange: resp['content-range'],
  });
  if (!res) return;
  rememberHeaders(d.url, headers);
  if (d.tabId < 0) return;
  if (res.kind === 'segment') { noteSegment(d.tabId, d.url, res, headers); return; }
  addItem(d.tabId, {
    url: d.url, kind: res.kind, media: res.media, ext: res.ext, size: res.size, filename: res.name,
    mime: VidHawkClassify.mimeBase(resp['content-type']), headers, frameUrl: d.initiator || d.documentUrl || '', source: 'network',
  });
}, { urls: ['<all_urls>'] }, ['responseHeaders', 'extraHeaders']);

chrome.webRequest.onCompleted.addListener((d) => reqHeaders.delete(d.requestId), { urls: ['<all_urls>'] });
chrome.webRequest.onErrorOccurred.addListener((d) => reqHeaders.delete(d.requestId), { urls: ['<all_urls>'] });

// ---------------------------------------------------------------- navigation
chrome.webNavigation.onCommitted.addListener((d) => {
  if (d.frameId !== 0) return;
  const tab = state.tabs[d.tabId];
  const next = stripHash(d.url);
  if (!tab || tab.url !== next || d.transitionType === 'reload') resetTab(d.tabId, d.url);
});
chrome.webNavigation.onHistoryStateUpdated.addListener((d) => {
  if (d.frameId !== 0) return;
  const tab = tabState(d.tabId);
  const next = stripHash(d.url);
  if (tab.url === next) return;
  tab.url = next;
  for (const it of Object.values(tab.items)) it.stale = true;
  tab.segments = null;
  updateBadge(d.tabId);
  persist();
  notifyPopup();
});
chrome.tabs.onRemoved.addListener((tabId) => { delete state.tabs[tabId]; persist(); });

// --------------------------------------------------------- layers 2/3 (page)
function kindFromUrl(url) {
  const ext = VidHawkClassify.urlExt(url);
  if (ext === 'm3u8') return { kind: 'hls', ext };
  if (ext === 'mpd') return { kind: 'dash', ext };
  if (ext === 'torrent') return { kind: 'torrent', ext };
  if (VidHawkClassify.VIDEO.has(ext)) return { kind: 'file', media: 'video', ext };
  if (VidHawkClassify.AUDIO.has(ext)) return { kind: 'file', media: 'audio', ext };
  return null;
}
function handlePageReport(sender, msg) {
  const tabId = sender.tab?.id;
  if (tabId == null || tabId < 0) return;
  const pageUrl = msg.pageUrl || sender.url || '';
  switch (msg.type) {
    case 'meta':
      if (sender.frameId === 0) { tabState(tabId).meta = msg.meta || {}; persist(); notifyPopup(); }
      break;
    case 'manifest': {
      if (!/^https?:/i.test(msg.url)) break;
      addItem(tabId, { url: msg.url, kind: msg.kind, ext: msg.kind === 'hls' ? 'm3u8' : 'mpd', isMaster: !!msg.master,
        headers: headersFor(msg.url, pageUrl), frameUrl: pageUrl, source: 'page', verified: true });
      if (msg.master && msg.children?.length) markChildren(tabId, msg.url, msg.children);
      break;
    }
    case 'media-src': {
      if (!/^https?:/i.test(msg.url)) break;
      const k = kindFromUrl(msg.url) || { kind: 'file', media: msg.tag === 'audio' ? 'audio' : 'video', ext: msg.tag === 'audio' ? 'm4a' : 'mp4' };
      const it = addItem(tabId, { url: msg.url, ...k, headers: headersFor(msg.url, pageUrl), frameUrl: pageUrl, source: 'player', verified: k.kind !== 'file' || !!kindFromUrl(msg.url) });
      it.playing = Date.now();
      persist();
      break;
    }
    case 'api-media': {
      for (const url of (msg.urls || []).slice(0, 20)) {
        const k = kindFromUrl(url);
        if (!k || !/^https?:/i.test(url)) continue;
        const tab = tabState(tabId);
        if (tab.items[stripHash(url)]) continue;
        addItem(tabId, { url, ...k, headers: headersFor(url, pageUrl), frameUrl: pageUrl, source: 'api', verified: false });
      }
      break;
    }
  }
}

// ----------------------------------------------------------- native engine
let port = null;
let nextId = 1;
const pending = new Map();

function connect() {
  if (port) return port;
  port = chrome.runtime.connectNative(HOST);
  port.onMessage.addListener(onNative);
  port.onDisconnect.addListener(() => {
    const msg = chrome.runtime.lastError?.message || 'engine disconnected';
    port = null;
    state.engine.connected = false;
    state.engine.error = msg;
    for (const [, p] of pending) p.reject(new Error(msg));
    pending.clear();
    notifyPopup();
  });
  return port;
}
function call(msg, timeout = 30000) {
  return new Promise((resolve, reject) => {
    const id = nextId++;
    pending.set(id, { resolve, reject });
    try {
      connect().postMessage({ id, ...msg });
    } catch (e) {
      pending.delete(id);
      reject(e);
      return;
    }
    setTimeout(() => { if (pending.delete(id)) reject(new Error('engine did not answer in time')); }, timeout);
  });
}
function onNative(m) {
  if (m.id && pending.has(m.id)) {
    const p = pending.get(m.id);
    pending.delete(m.id);
    if (m.ok === false) p.reject(new Error(m.error || 'engine error'));
    else p.resolve(m);
  }
  if (m.event === 'job' && m.job) applyJob(m.job);
}
function applyJob(job) {
  const prev = state.jobs[job.id];
  if (job.status === 'removed') delete state.jobs[job.id];
  else state.jobs[job.id] = job;
  if (job.status === 'done' && prev?.status !== 'done') onJobDone(job);
  if (job.status === 'error' && prev?.status !== 'error') notify(`Download failed`, `${job.title || job.url}\n${job.error || ''}`);
  persist();
  notifyPopup();
}
async function ping() {
  try {
    const r = await call({ cmd: 'ping' }, 8000);
    state.engine = { connected: true, info: r.engine, lastPing: Date.now(), error: null };
    const seen = new Set();
    for (const j of r.jobs || []) { state.jobs[j.id] = j; seen.add(j.id); }
    for (const id of Object.keys(state.jobs)) if (!seen.has(id)) delete state.jobs[id];
    persist();
  } catch (e) {
    state.engine.connected = false;
    state.engine.error = e.message;
  }
  notifyPopup();
  return state.engine;
}
setInterval(() => {
  if (port && Object.values(state.jobs).some((j) => j.status === 'running' || j.status === 'queued')) call({ cmd: 'ping' }, 8000).catch(() => {});
}, 20000);
ready.then(ping);

async function startDownload(spec) {
  const r = await call({ cmd: 'download', ...spec }, 15000);
  applyJob(r.job);
  return r.job;
}
function onJobDone(job) {
  if (!state.settings.notifications) return;
  const id = `vh-${job.id}`;
  notificationPaths.set(id, job.path);
  chrome.notifications.create(id, {
    type: 'basic', iconUrl: 'icons/128.png', title: 'VidHawk — download complete',
    message: job.title || job.path || job.url, contextMessage: 'Click to show in folder',
  }, () => void chrome.runtime.lastError);
}
function notify(title, message) {
  if (!state.settings.notifications) return;
  chrome.notifications.create({ type: 'basic', iconUrl: 'icons/128.png', title, message }, () => void chrome.runtime.lastError);
}
chrome.notifications.onClicked.addListener((id) => {
  const path = notificationPaths.get(id);
  if (path) call({ cmd: 'open', path }).catch(() => {});
  chrome.notifications.clear(id);
});
function notifyPopup() {
  chrome.runtime.sendMessage({ type: 'state-changed' }).catch(() => {});
}

// ------------------------------------------------------- browser downloads
// Downloads started by a "Download" button are navigations that turn into a
// browser download, often in a tab that closes immediately — so they are kept
// in a tab-independent list and, when enabled, handed to the engine.
const MAX_RECENT = 20;
function rememberDownload(item) {
  const url = item.finalUrl || item.url;
  if (!/^https?:/i.test(url)) return null;
  state.recentDownloads ||= [];
  const entry = {
    id: item.id, url, filename: item.filename || '', mime: VidHawkClassify.mimeBase(item.mime), size: item.totalBytes > 0 ? item.totalBytes : null,
    referrer: item.referrer || '', headers: headersFor(url, item.referrer), added: Date.now(), takenOver: false,
  };
  state.recentDownloads = [entry, ...state.recentDownloads.filter((d) => d.url !== url)].slice(0, MAX_RECENT);
  persist();
  notifyPopup();
  return entry;
}
function shouldTakeOver(item) {
  const url = item.finalUrl || item.url;
  const ext = VidHawkClassify.urlExt(item.filename || '') || VidHawkClassify.urlExt(url);
  const mime = VidHawkClassify.mimeBase(item.mime);
  const media = /^(video|audio)\//.test(mime);
  const big = item.totalBytes < 0 || item.totalBytes >= 1024 * 1024;
  if (ext === 'torrent' || ext === 'm3u8' || ext === 'mpd') return ext;
  if ((TAKEOVER_EXTS.has(ext) || media) && big) return ext || (media ? 'media' : 'file');
  return null;
}
function basename(p) {
  return (p || '').split(/[\\/]/).pop();
}
// onCreated always fires (onDeterminingFilename can be bypassed, e.g. by
// automation or enterprise download policies), so record and take over here.
chrome.downloads.onCreated.addListener((item) => {
  const entry = rememberDownload({ ...item, filename: basename(item.filename) });
  if (!entry || !state.settings.takeOver || !state.engine.connected) return;
  const ext = shouldTakeOver(item);
  if (!ext) return;
  chrome.downloads.cancel(item.id, () => chrome.downloads.erase({ id: item.id }, () => void chrome.runtime.lastError));
  const kind = ext === 'torrent' ? 'torrent' : ext === 'm3u8' ? 'hls' : ext === 'mpd' ? 'dash' : 'file';
  entry.takenOver = true;
  persist();
  startDownload({ url: entry.url, kind, title: entry.filename || '', headers: entry.headers, pageUrl: item.referrer || '', quality: 'best', expectedSize: entry.size || 0 })
    .catch((e) => notify('VidHawk could not take over the download', e.message));
});
// Chrome decides the file name slightly later; keep the recorded entry in sync.
chrome.downloads.onDeterminingFilename.addListener((item, suggest) => {
  suggest();
  const entry = (state.recentDownloads || []).find((d) => d.id === item.id);
  if (entry && !entry.filename && item.filename) { entry.filename = basename(item.filename); persist(); notifyPopup(); }
});
chrome.downloads.onChanged.addListener((delta) => {
  if (!delta.filename?.current) return;
  const entry = (state.recentDownloads || []).find((d) => d.id === delta.id);
  if (entry && !entry.filename) { entry.filename = basename(delta.filename.current); persist(); notifyPopup(); }
});

// ------------------------------------------------------------ context menus
function createMenus() {
  chrome.contextMenus.removeAll(() => {
    chrome.contextMenus.create({ id: 'vh-link', title: 'Download with VidHawk', contexts: ['link', 'video', 'audio', 'image'] });
    chrome.contextMenus.create({ id: 'vh-page', title: 'Download video from this page (yt-dlp)', contexts: ['page', 'frame'] });
  });
}
chrome.runtime.onInstalled.addListener(createMenus);
chrome.runtime.onStartup.addListener(createMenus);
chrome.contextMenus.onClicked.addListener((info, tab) => {
  const pageUrl = info.frameUrl || info.pageUrl || tab?.url || '';
  if (info.menuItemId === 'vh-page') {
    startDownload({ url: pageUrl, kind: 'ytdlp', title: '', pageUrl, headers: {}, quality: 'best' })
      .catch((e) => notify('VidHawk', e.message));
    return;
  }
  const url = info.linkUrl || info.srcUrl;
  if (!url) return;
  const kind = url.startsWith('magnet:') ? 'torrent' : 'auto';
  const title = kind === 'torrent' ? '' : VidHawkNaming.sanitize((info.linkText || '').trim()).slice(0, 120);
  startDownload({ url, kind, title: title === 'download' ? '' : title, headers: headersFor(url, pageUrl), pageUrl, quality: 'best' })
    .catch((e) => notify('VidHawk', e.message));
});

// --------------------------------------------------------------- messaging
chrome.runtime.onMessage.addListener((msg, sender, sendResponse) => {
  (async () => {
    await ready;
    switch (msg.type) {
      case 'meta': case 'manifest': case 'media-src': case 'api-media':
        handlePageReport(sender, msg);
        return null;
      case 'get-tab': {
        const tab = state.tabs[msg.tabId] || tabState(msg.tabId);
        const fresh = await Promise.race([
          chrome.tabs.sendMessage(msg.tabId, { type: 'get-meta' }, { frameId: 0 }).catch(() => null),
          new Promise((r) => setTimeout(() => r(null), 400)),
        ]);
        if (fresh && typeof fresh === 'object') { tab.meta = fresh; persist(); }
        return { tab, jobs: state.jobs, engine: state.engine, settings: state.settings, recentDownloads: state.recentDownloads || [] };
      }
      case 'forget-download':
        state.recentDownloads = (state.recentDownloads || []).filter((d) => d.url !== msg.url);
        persist();
        return true;
      case 'ping':
        return await ping();
      case 'download':
        return await startDownload(msg.spec);
      case 'probe': {
        const r = await call({ cmd: 'probe', url: msg.url, headers: msg.headers || {} }, 45000);
        const tab = msg.tabId != null && state.tabs[msg.tabId];
        const item = tab && tab.items[stripHash(msg.url)];
        if (item) {
          item.probe = r.probe;
          item.verified = true;
          if (r.probe.kind && r.probe.kind !== 'ytdlp') item.kind = r.probe.kind;
          if (r.probe.master) { item.isMaster = true; markChildren(msg.tabId, msg.url, (r.probe.variants || []).map((v) => v.url).filter(Boolean)); }
          persist();
        }
        return r.probe;
      }
      case 'job-action': {
        await call({ cmd: msg.action, jobId: msg.jobId, deleteFile: !!msg.deleteFile }, 10000);
        if (msg.action === 'remove') { delete state.jobs[msg.jobId]; persist(); }
        return true;
      }
      case 'open':
        await call({ cmd: 'open', path: msg.path }, 10000);
        return true;
      case 'clear-finished':
        for (const [id, j] of Object.entries(state.jobs)) {
          if (j.status !== 'running' && j.status !== 'queued') { call({ cmd: 'remove', jobId: id }, 10000).catch(() => {}); delete state.jobs[id]; }
        }
        persist();
        return true;
      case 'remove-item': {
        const t = state.tabs[msg.tabId];
        if (t) { delete t.items[msg.key]; updateBadge(msg.tabId); persist(); }
        return true;
      }
      case 'settings':
        Object.assign(state.settings, msg.settings || {});
        chrome.storage.local.set({ settings: state.settings }).catch(() => {});
        return state.settings;
      case 'state-changed':
        return null;
    }
    return null;
  })().then(
    (result) => sendResponse({ ok: true, result }),
    (e) => sendResponse({ ok: false, error: e?.message || String(e) }),
  );
  return true;
});
