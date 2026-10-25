// Screenshot the first page of a Chromium/Electron instance over CDP.
// Usage: node tools/e2e/screenshot.cjs <debug-port> <out.png> [width] [height]
const fs = require('node:fs');

const [,, port = '9335', out = 'screenshot.png', width = '1120', height = '740'] = process.argv;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  let targets = [];
  for (let i = 0; i < 40; i++) {
    try {
      targets = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      if (targets.some((t) => t.type === 'page')) break;
    } catch {}
    await sleep(500);
  }
  const page = targets.find((t) => t.type === 'page');
  if (!page) throw new Error('no page target');
  const ws = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let id = 0;
  const pending = new Map();
  ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); } };
  const send = (method, params = {}) => new Promise((res) => { pending.set(++id, res); ws.send(JSON.stringify({ id, method, params })); });
  await send('Emulation.setDeviceMetricsOverride', { width: Number(width), height: Number(height), deviceScaleFactor: 1, mobile: false });
  await sleep(2500);
  const title = (await send('Runtime.evaluate', { expression: 'document.title + " | " + (document.querySelector(".engine-status")?.textContent || "")', returnByValue: true })).result?.result?.value;
  console.log('page:', page.url, '|', title);
  const shot = await send('Page.captureScreenshot', { format: 'png' });
  fs.writeFileSync(out, Buffer.from(shot.result.data, 'base64'));
  console.log('screenshot:', out);
  ws.close();
  process.exit(0);
})().catch((e) => { console.error('screenshot failed:', e.message); process.exit(1); });
