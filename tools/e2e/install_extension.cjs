// Installs the VidHawk extension (unpacked) into a Chrome started with
// --remote-debugging-port=<port> --enable-unsafe-extension-debugging.
// Usage: node tools/e2e/install_extension.cjs <port> [extension-dir]
const path = require('node:path');

const [,, port = '9222', extDir = path.resolve(__dirname, '..', '..', 'extension')] = process.argv;
const EXT_ID = 'nbpppdcnpheeihphpfihofafmhmnhhfd';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  let version = null;
  for (let i = 0; i < 40 && !version; i++) {
    try { version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); } catch { await sleep(500); }
  }
  if (!version) throw new Error(`Chrome debug port ${port} not reachable`);
  const ws = new WebSocket(version.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  const pending = new Map();
  let id = 0;
  ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
  const send = (method, params = {}) => new Promise((res) => { pending.set(++id, res); ws.send(JSON.stringify({ id, method, params })); });
  const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
  if (list.some((t) => t.url.startsWith(`chrome-extension://${EXT_ID}/`))) {
    console.log('VidHawk extension already present in this profile');
  } else {
    const r = await send('Extensions.loadUnpacked', { path: path.resolve(extDir) });
    if (r.error) throw new Error(r.error.message);
    console.log('installed extension id:', r.result.id);
  }
  ws.close();
  process.exit(0);
})().catch((e) => { console.error('install failed:', e.message); process.exit(1); });
