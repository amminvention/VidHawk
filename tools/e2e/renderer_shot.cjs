// Screenshot the desktop renderer in headless Chrome with a shim for the
// Electron preload bridge, so the UI can be reviewed without opening a window.
// Usage: node tools/e2e/renderer_shot.cjs <chrome-debug-port> <out.png>
// Requires a running engine (`vidhawk serve`) so ~/.vidhawk/api.json exists.
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const [,, port = '9336', out = 'renderer.png'] = process.argv;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const apiPath = path.join(process.env.VIDHAWK_HOME || path.join(os.homedir(), '.vidhawk'), 'api.json');
const endpoint = JSON.parse(fs.readFileSync(apiPath, 'utf8'));
const indexUrl = 'file:///' + path.resolve(__dirname, '..', '..', 'desktop', 'out', 'renderer', 'index.html').replace(/\\/g, '/');

const shim = `window.vidhawk = {
  endpoint: async () => ${JSON.stringify(endpoint)},
  enginePath: async () => 'vidhawk.exe',
  onEndpoint: () => () => {},
  onAddUrl: () => () => {},
  showItem: async () => {}, openExternal: async () => {}, pickDirectory: async () => null,
  version: async () => '0.1.0', loginItem: { get: async () => false, set: async () => {} }, pathForFile: () => '',
};`;

(async () => {
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  const bws = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((res, rej) => { bws.onopen = res; bws.onerror = rej; });
  let id = 0;
  const pending = new Map();
  const mk = (ws) => (method, params = {}, sessionId) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
  bws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
  const send = mk(bws);
  const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' });
  const { result: { sessionId } } = await send('Target.attachToTarget', { targetId, flatten: true });
  await send('Page.enable', {}, sessionId);
  await send('Page.addScriptToEvaluateOnNewDocument', { source: shim }, sessionId);
  await send('Emulation.setDeviceMetricsOverride', { width: 1120, height: 740, deviceScaleFactor: 1, mobile: false }, sessionId);
  await send('Page.navigate', { url: indexUrl }, sessionId);
  await sleep(4000);
  const status = (await send('Runtime.evaluate', { expression: 'document.querySelector(".engine-status")?.textContent', returnByValue: true }, sessionId)).result?.result?.value;
  console.log('engine status in UI:', status);
  const shot = await send('Page.captureScreenshot', { format: 'png' }, sessionId);
  fs.writeFileSync(out, Buffer.from(shot.result.data, 'base64'));
  console.log('screenshot:', out);
  await send('Target.closeTarget', { targetId });
  bws.close();
  process.exit(0);
})().catch((e) => { console.error('renderer screenshot failed:', e.message); process.exit(1); });
