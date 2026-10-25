// End-to-end check of the extension + engine in a real Chrome over CDP.
// Usage: node tools/e2e/extension_e2e.cjs <debug-port> <page-url> <out-dir>
// Chrome must be running with --remote-debugging-port=<port> --enable-unsafe-extension-debugging.
const fs = require('node:fs');
const path = require('node:path');

const [,, portArg = '9333', pageUrl = 'https://aryplus.tv/series/6a57868b5bf57c474cc00a50', outDir = '.'] = process.argv;
const port = Number(portArg);
const EXT_ID = 'nbpppdcnpheeihphpfihofafmhmnhhfd';
const EXT_DIR = path.resolve(__dirname, '..', '..', 'extension');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map(); this.events = []; ws.onmessage = (e) => this.onMessage(JSON.parse(e.data)); }
  static async connect(url) {
    const ws = new WebSocket(url);
    await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws connect failed ' + url)); });
    return new CDP(ws);
  }
  onMessage(m) {
    if (m.id && this.pending.has(m.id)) { const p = this.pending.get(m.id); this.pending.delete(m.id); m.error ? p.reject(new Error(m.error.message)) : p.resolve(m.result); }
    else this.events.push(m);
  }
  send(method, params = {}) {
    const id = ++this.id;
    return new Promise((resolve, reject) => { this.pending.set(id, { resolve, reject }); this.ws.send(JSON.stringify({ id, method, params })); });
  }
  async eval(expression) {
    const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || 'eval failed');
    return r.result.value;
  }
  close() { this.ws.close(); }
}

async function json(p) { const r = await fetch(`http://127.0.0.1:${port}${p}`); return r.json(); }

async function waitForChrome() {
  for (let i = 0; i < 60; i++) { try { return await json('/json/version'); } catch { await sleep(500); } }
  throw new Error('Chrome debug port not reachable');
}

async function findTarget(pred, timeoutMs = 30000) {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const list = await json('/json/list');
    const t = list.find(pred);
    if (t) return t;
    await sleep(500);
  }
  return null;
}

(async () => {
  const version = await waitForChrome();
  console.log('chrome:', version.Browser);
  const browser = await CDP.connect(version.webSocketDebuggerUrl);

  let sw = await findTarget((t) => t.type === 'service_worker' && t.url.startsWith(`chrome-extension://${EXT_ID}/`), 3000);
  if (!sw) {
    console.log('loading unpacked extension from', EXT_DIR);
    await browser.send('Extensions.loadUnpacked', { path: EXT_DIR });
    sw = await findTarget((t) => t.type === 'service_worker' && t.url.startsWith(`chrome-extension://${EXT_ID}/`), 20000);
  }
  if (!sw) throw new Error('extension service worker not found');
  console.log('service worker:', sw.url);
  const swc = await CDP.connect(sw.webSocketDebuggerUrl);
  for (let i = 0; i < 40; i++) {
    if (await swc.eval("typeof ping === 'function'").catch(() => false)) break;
    await sleep(250);
  }

  const engine = await swc.eval('ping().then(e => JSON.stringify(e))');
  console.log('engine:', engine);

  const { targetId } = await browser.send('Target.createTarget', { url: pageUrl });
  const pageTarget = await findTarget((t) => t.id === targetId);
  const page = await CDP.connect(pageTarget.webSocketDebuggerUrl);
  await page.send('Page.enable');
  const tabId = await swc.eval(`new Promise(res => chrome.tabs.query({url: ${JSON.stringify(pageUrl)} + '*'}, ts => res(ts[0]?.id)))`);
  console.log('tab id:', tabId);

  let tab = null;
  for (let i = 0; i < 60; i++) {
    tab = JSON.parse(await swc.eval(`JSON.stringify(state.tabs[${tabId}] || null)`));
    const items = Object.values(tab?.items || {});
    if (items.some((it) => it.kind === 'hls' || it.kind === 'dash' || it.kind === 'file')) break;
    await sleep(1000);
  }
  // Let the SPA finish setting its title / loading the player before reading.
  await sleep(Number(process.env.VH_SETTLE_MS || 8000));
  if (process.env.VH_PLAY === '1') {
    // Behave like a user: click the play overlay / video, then call play() as a fallback.
    const played = await page.eval(`(async () => {
      const clicks = [];
      const sel = ['.plyr__control--overlaid', '[class*="play-button"]', '[class*="playbutton"]', '[class*="play_button"]', '[class*="overlay"] button', 'button[aria-label*="Play" i]', '.vjs-big-play-button', 'video'];
      for (const s of sel) { const el = document.querySelector(s); if (el) { clicks.push(s); el.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, view: window })); await new Promise(r => setTimeout(r, 800)); } }
      const v = document.querySelector('video');
      if (v) { try { v.muted = true; await v.play(); } catch (e) { clicks.push('play(): ' + e.message); } }
      return { clicks, src: v ? (v.currentSrc || v.src || null) : null, ready: v ? v.readyState : null, videos: document.querySelectorAll('video').length, iframes: [...document.querySelectorAll('iframe')].map(f => f.src).filter(Boolean).slice(0, 5) };
    })()`).catch((e) => ({ error: e.message }));
    console.log('play attempt:', JSON.stringify(played));
    await sleep(Number(process.env.VH_PLAY_WAIT_MS || 10000));
    tab = JSON.parse(await swc.eval(`JSON.stringify(state.tabs[${tabId}] || null)`));
  }
  tab = JSON.parse(await swc.eval(`JSON.stringify(state.tabs[${tabId}] || null)`));
  const items = Object.values(tab?.items || {});
  console.log('meta:', JSON.stringify(tab?.meta));
  console.log('items:', items.length);
  for (const it of items) console.log(`  [${it.kind}${it.childOf ? ' child' : ''}${it.isMaster ? ' master' : ''}] ${it.source} ${it.url}`);
  console.log('segments:', JSON.stringify(tab?.segments));

  const popupUrl = `chrome-extension://${EXT_ID}/popup.html?tab=${tabId}`;
  const { targetId: popupId } = await browser.send('Target.createTarget', { url: popupUrl });
  const popupTarget = await findTarget((t) => t.id === popupId);
  const popup = await CDP.connect(popupTarget.webSocketDebuggerUrl);
  await popup.send('Page.enable');
  await popup.send('Emulation.setDeviceMetricsOverride', { width: 392, height: 600, deviceScaleFactor: 2, mobile: false });
  await sleep(4000);
  if (process.env.VH_OPEN_OPTIONS === '1') {
    await popup.eval(`(() => { const b = document.querySelector('#mediaList [data-action="options"]'); if (b) b.click(); const s = document.querySelector('#mediaList [data-role="series"]'); if (s && !s.checked) { s.click(); } return true; })()`);
    await sleep(500);
  }
  const shot = await popup.send('Page.captureScreenshot', { format: 'png' });
  const shotPath = path.join(outDir, 'popup.png');
  fs.writeFileSync(shotPath, Buffer.from(shot.data, 'base64'));
  console.log('screenshot:', shotPath);
  const names = await popup.eval(`[...document.querySelectorAll('#mediaList .card')].map(c => ({ kind: c.querySelector('.kind').textContent, name: c.querySelector('.name').value, qualities: [...c.querySelectorAll('.quality option')].map(o => o.textContent) }))`);
  console.log('popup cards:', JSON.stringify(names, null, 1));

  if (process.env.VH_DOWNLOAD !== '0' && names.length) {
    const quality = process.env.VH_QUALITY || 'worst';
    await popup.eval(`(() => { const c = document.querySelector('#mediaList .card'); const q = c.querySelector('.quality'); if (!q.hidden) { const opts = [...q.options]; const pick = ${JSON.stringify(quality)} === 'worst' ? opts[opts.length - 1] : opts.find(o => o.value === ${JSON.stringify(quality)}) || opts[0]; q.value = pick.value; q.dispatchEvent(new Event('change')); } c.querySelector('[data-action="download"]').click(); return true; })()`);
    console.log('download clicked; waiting for the engine…');
    let job = null;
    for (let i = 0; i < 300; i++) {
      await sleep(1000);
      const jobs = JSON.parse(await swc.eval('JSON.stringify(Object.values(state.jobs))'));
      job = jobs.sort((a, b) => b.created - a.created)[0];
      if (!job) continue;
      if (i % 5 === 0) console.log(`  ${job.status} ${job.percent ?? ''}% ${job.done}/${job.total} ${job.unit} ${Math.round((job.speed || 0) / 1024)} KB/s`);
      if (job.status === 'done' || job.status === 'error' || job.status === 'cancelled') break;
    }
    console.log('job:', JSON.stringify(job));
    await sleep(1500);
    const shot2 = await popup.send('Page.captureScreenshot', { format: 'png' });
    fs.writeFileSync(path.join(outDir, 'popup_after.png'), Buffer.from(shot2.data, 'base64'));
    console.log('screenshot:', path.join(outDir, 'popup_after.png'));
  }

  const log = await swc.eval('JSON.stringify(state.engine)');
  console.log('engine state:', log);
  if (process.env.VH_KEEP !== '1') await browser.send('Browser.close');
  process.exit(0);
})().catch((e) => { console.error('E2E FAILED:', e.message); process.exit(1); });
