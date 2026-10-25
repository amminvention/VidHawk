import { useSignal } from '@preact/signals';
import type { JSX } from 'preact';
import { useEffect } from 'preact/hooks';
import { api, config, connected, engine, type Config, type Integration } from '../api';
import { Button, Icon } from '../components/ui';

export function Settings(): JSX.Element {
  const draft = useSignal<Config | null>(config.value);
  const saved = useSignal('');
  const error = useSignal('');
  const integration = useSignal<Integration | null>(null);
  const loginItem = useSignal(false);
  const enginePath = useSignal('');
  const appVersion = useSignal('');

  useEffect(() => {
    if (!draft.value && config.value) draft.value = config.value;
  }, [config.value]);

  useEffect(() => {
    window.vidhawk.loginItem.get().then((v) => (loginItem.value = v));
    window.vidhawk.enginePath().then((p) => (enginePath.value = p));
    window.vidhawk.version().then((v) => (appVersion.value = v));
    if (connected.value) api.integration().then((i) => (integration.value = i)).catch(() => {});
  }, [connected.value]);

  const d = draft.value;
  if (!d) return <section class="page"><header class="page-head"><h1>Settings</h1></header><p class="muted">Waiting for the engine…</p></section>;

  const set = <K extends keyof Config>(k: K, v: Config[K]) => { draft.value = { ...d, [k]: v }; saved.value = ''; };
  const setT = <K extends keyof Config['torrent']>(k: K, v: Config['torrent'][K]) => { draft.value = { ...d, torrent: { ...d.torrent, [k]: v } }; saved.value = ''; };

  async function save() {
    error.value = '';
    try {
      const { ...patch } = d;
      config.value = await api.updateConfig(patch as unknown as Record<string, unknown>);
      draft.value = config.value;
      saved.value = 'Saved';
    } catch (e) {
      error.value = (e as Error).message;
    }
  }

  async function install() {
    error.value = '';
    try {
      const r = await api.installIntegration();
      integration.value = { ...(integration.value || { manifest: '', extensionId: '' }), browsers: r.browsers };
      saved.value = `Browser integration registered for ${r.installed.join(', ')}`;
    } catch (e) {
      error.value = (e as Error).message;
    }
  }

  const num = (e: Event) => Number((e.target as HTMLInputElement).value);
  return (
    <section class="page">
      <header class="page-head"><h1>Settings</h1><span class="spacer" />{saved.value && <span class="ok-text"><Icon name="check" /> {saved.value}</span>}<Button variant="primary" onClick={save}>Save</Button></header>
      {error.value && <p class="form-error"><Icon name="alert" /> {error.value}</p>}

      <div class="card">
        <h2>Downloads</h2>
        <label class="field">
          <span>Download folder</span>
          <div class="input-row">
            <input type="text" value={d.downloadDir} onInput={(e) => set('downloadDir', (e.target as HTMLInputElement).value)} />
            <Button icon="folder" onClick={async () => { const p = await window.vidhawk.pickDirectory(d.downloadDir); if (p) set('downloadDir', p); }}>Browse</Button>
          </div>
        </label>
        <div class="grid-3">
          <label class="field"><span>Connections per file</span><input type="number" min={1} max={32} value={d.connections} onInput={(e) => set('connections', num(e))} /></label>
          <label class="field"><span>Parallel segments (HLS/DASH)</span><input type="number" min={1} max={32} value={d.segmentConcurrency} onInput={(e) => set('segmentConcurrency', num(e))} /></label>
          <label class="field"><span>Simultaneous downloads</span><input type="number" min={1} max={10} value={d.maxJobs} onInput={(e) => set('maxJobs', num(e))} /></label>
        </div>
        <label class="check"><input type="checkbox" checked={d.verifyTLS} onChange={(e) => set('verifyTLS', (e.target as HTMLInputElement).checked)} /> Verify TLS certificates</label>
      </div>

      <div class="card">
        <h2>Tools</h2>
        <div class="grid-2">
          <label class="field"><span>ffmpeg path <small>{engine.value?.ffmpeg ? 'detected' : 'not found — HLS/DASH will be saved without remuxing'}</small></span><input type="text" value={d.ffmpegPath} onInput={(e) => set('ffmpegPath', (e.target as HTMLInputElement).value)} placeholder="auto-detect" /></label>
          <label class="field"><span>yt-dlp path <small>{engine.value?.ytdlp ? 'detected' : 'optional — needed for YouTube-class sites'}</small></span><input type="text" value={d.ytDlpPath} onInput={(e) => set('ytDlpPath', (e.target as HTMLInputElement).value)} placeholder="auto-detect" /></label>
        </div>
      </div>

      <div class="card">
        <h2>Torrents</h2>
        <div class="grid-2">
          <label class="field"><span>Listen port</span><input type="number" min={1024} max={65535} value={d.torrent.listenPort} onInput={(e) => setT('listenPort', num(e))} /></label>
          <label class="check"><input type="checkbox" checked={d.torrent.seedAfterComplete} onChange={(e) => setT('seedAfterComplete', (e.target as HTMLInputElement).checked)} /> Keep seeding after completion</label>
        </div>
      </div>

      <div class="card">
        <h2>Browser integration</h2>
        <p class="muted">The VidHawk extension talks to this engine through the browser's native-messaging host. Extension ID <code>{integration.value?.extensionId || 'nbpppdcnpheeihphpfihofafmhmnhhfd'}</code>.</p>
        <ul class="browsers">
          {Object.entries(integration.value?.browsers || {}).map(([b, ok]) => (
            <li key={b} class={ok ? 'ok' : ''}><Icon name={ok ? 'check' : 'x'} /> {b}</li>
          ))}
        </ul>
        <div class="row">
          <Button icon="refresh" onClick={install} disabled={!connected.value}>Register for all browsers</Button>
          <Button variant="ghost" icon="external" onClick={() => window.vidhawk.openExternal('https://github.com/amminvention/VidHawk#install-the-browser-extension')}>How to load the extension</Button>
        </div>
      </div>

      <div class="card">
        <h2>Application</h2>
        <label class="check"><input type="checkbox" checked={loginItem.value} onChange={async (e) => { const v = (e.target as HTMLInputElement).checked; await window.vidhawk.loginItem.set(v); loginItem.value = v; }} /> Start VidHawk when I sign in</label>
        <p class="muted">App {appVersion.value} · Engine {engine.value?.version || '—'} · <code>{enginePath.value}</code></p>
      </div>
    </section>
  );
}
