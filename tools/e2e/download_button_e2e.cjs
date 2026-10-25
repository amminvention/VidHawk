// Reproduces a "Download" button that opens a new tab which turns into a browser
// download (attachment, no file extension in the URL) and checks that VidHawk
// records it and, with take-over enabled, hands it to the engine.
// Usage: node tools/e2e/download_button_e2e.cjs <chrome-debug-port> <sample-file>
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');

const [,, portArg = '9339', sample = path.resolve(__dirname, '..', '..', 'engine', 'dist', 'livetest', 'Big_Buck_Bunny_360_10s_1MB.mp4')] = process.argv;
const port = Number(portArg);
const EXT_ID = 'nbpppdcnpheeihphpfihofafmhmnhhfd';
const EXT_DIR = path.resolve(__dirname, '..', '..', 'extension');
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pending = new Map(); ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && this.pending.has(m.id)) { const p = this.pending.get(m.id); this.pending.delete(m.id); m.error ? p.reject(new Error(m.error.message)) : p.resolve(m.result); } }; }
  static async connect(url) { const ws = new WebSocket(url); await new Promise((res, rej) => { ws.onopen = res; ws.onerror = () => rej(new Error('ws failed')); }); return new CDP(ws); }
  send(method, params = {}) { const id = ++this.id; return new Promise((resolve, reject) => { this.pending.set(id, { resolve, reject }); this.ws.send(JSON.stringify({ id, method, params })); }); }
  async eval(expression) { const r = await this.send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true }); if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || 'eval failed'); return r.result.value; }
}
const json = async (p) => (await fetch(`http://127.0.0.1:${port}${p}`)).json();
async function findTarget(pred, timeoutMs = 20000) { const start = Date.now(); while (Date.now() - start < timeoutMs) { const t = (await json('/json/list')).find(pred); if (t) return t; await sleep(400); } return null; }

(async () => {
  // Take-over only applies to media ≥ 1 MB; the sample is just under, so serve it twice.
  const data = Buffer.concat([fs.readFileSync(sample), fs.readFileSync(sample)]);
  const server = http.createServer((req, res) => {
    if (req.url.startsWith('/dl')) {
      res.writeHead(200, { 'Content-Type': 'video/mp4', 'Content-Length': data.length, 'Accept-Ranges': 'bytes', 'Content-Disposition': 'attachment; filename="sample clip.mp4"' });
      res.end(data);
      return;
    }
    res.writeHead(200, { 'Content-Type': 'text/html' });
    res.end('<!doctype html><title>Download button test</title><h1>Sample</h1><button id="dl" onclick="window.open(\'/dl/abc123?dl=1\')">Download</button>');
  }).listen(0, '127.0.0.1');
  await new Promise((r) => server.once('listening', r));
  const base = `http://127.0.0.1:${server.address().port}`;
  console.log('test server:', base);

  const version = await json('/json/version');
  const browser = await CDP.connect(version.webSocketDebuggerUrl);
  let sw = await findTarget((t) => t.type === 'service_worker' && t.url.startsWith(`chrome-extension://${EXT_ID}/`), 2000);
  if (!sw) { await browser.send('Extensions.loadUnpacked', { path: EXT_DIR }); sw = await findTarget((t) => t.type === 'service_worker' && t.url.startsWith(`chrome-extension://${EXT_ID}/`)); }
  const swc = await CDP.connect(sw.webSocketDebuggerUrl);
  for (let i = 0; i < 40 && !(await swc.eval("typeof ping === 'function'").catch(() => false)); i++) await sleep(250);
  console.log('engine:', await swc.eval('ping().then(e => e.connected)'));

  await browser.send('Browser.setDownloadBehavior', { behavior: 'allow', downloadPath: path.resolve(__dirname, '..', '..', 'engine', 'dist', 'livetest'), eventsEnabled: true }).catch(() => {});
  const { targetId } = await browser.send('Target.createTarget', { url: base + '/' });
  const pageTarget = await findTarget((t) => t.id === targetId);
  const page = await CDP.connect(pageTarget.webSocketDebuggerUrl);
  await page.send('Page.enable');
  await sleep(1500);
  // A real pointer click (user gesture) so window.open is not treated as a popup.
  const box = await page.eval("(() => { const r = document.getElementById('dl').getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2 }; })()");
  for (const type of ['mousePressed', 'mouseReleased']) await page.send('Input.dispatchMouseEvent', { type, x: box.x, y: box.y, button: 'left', clickCount: 1 });
  console.log('download button clicked at', JSON.stringify(box));

  let recent = [];
  let job = null;
  for (let i = 0; i < 60; i++) {
    await sleep(1000);
    recent = JSON.parse(await swc.eval('JSON.stringify(state.recentDownloads || [])'));
    const jobs = JSON.parse(await swc.eval('JSON.stringify(Object.values(state.jobs))'));
    job = jobs.filter((j) => j.url.startsWith(base)).sort((a, b) => b.created - a.created)[0] || null;
    if (job && (job.status === 'done' || job.status === 'error')) break;
  }
  const chromeDownloads = await swc.eval('new Promise(res => chrome.downloads.search({}, items => res(JSON.stringify(items.map(i => ({ url: i.url, filename: i.filename, state: i.state, mime: i.mime, bytes: i.totalBytes }))))))');
  console.log('chrome.downloads saw:', chromeDownloads);
  console.log('recorded browser downloads:', JSON.stringify(recent.map((d) => ({ filename: d.filename, mime: d.mime, size: d.size, takenOver: d.takenOver }))));
  console.log('engine job:', job ? `${job.status} ${job.title} -> ${job.path || job.error || ''}` : 'none');
  // Chrome never gets to name the file once we cancel its copy, so check the engine's result instead.
  const ok = recent.some((d) => d.takenOver && d.mime === 'video/mp4') && job && job.status === 'done' && job.title === 'sample clip.mp4' && fs.existsSync(job.path) && fs.statSync(job.path).size === data.length;
  console.log(ok ? 'PASS: button download recorded, taken over and completed with identical size' : 'FAIL');
  if (job?.path && fs.existsSync(job.path)) fs.unlinkSync(job.path);
  server.close();
  await browser.send('Browser.close').catch(() => {});
  process.exit(ok ? 0 : 1);
})().catch((e) => { console.error('E2E FAILED:', e.message); process.exit(1); });
