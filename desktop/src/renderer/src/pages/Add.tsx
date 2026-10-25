import { useSignal } from '@preact/signals';
import type { JSX } from 'preact';
import { useEffect } from 'preact/hooks';
import { api, config, connected, type ProbeResult } from '../api';
import { bytes, duration } from '../format';
import { Button, Icon } from '../components/ui';

export function Add({ initialUrl, onDone }: { initialUrl: string; onDone: () => void }): JSX.Element {
  const url = useSignal(initialUrl);
  const title = useSignal('');
  const quality = useSignal('best');
  const referer = useSignal('');
  const outDir = useSignal('');
  const probe = useSignal<ProbeResult | null>(null);
  const probing = useSignal(false);
  const error = useSignal('');
  const busy = useSignal(false);

  useEffect(() => { url.value = initialUrl; probe.value = null; }, [initialUrl]);

  const isMagnet = /^magnet:/i.test(url.value.trim());
  const canProbe = /^https?:\/\//i.test(url.value.trim());

  async function runProbe() {
    error.value = '';
    probing.value = true;
    try {
      const headers: Record<string, string> = {};
      if (referer.value) headers.referer = referer.value;
      probe.value = await api.probe(url.value.trim(), headers);
      if (!title.value && probe.value.filename) title.value = probe.value.filename;
    } catch (e) {
      error.value = (e as Error).message;
    } finally {
      probing.value = false;
    }
  }

  async function submit(e: Event) {
    e.preventDefault();
    error.value = '';
    const lines = url.value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean);
    if (!lines.length) return;
    busy.value = true;
    try {
      for (const u of lines) {
        const headers: Record<string, string> = {};
        if (referer.value) headers.referer = referer.value;
        await api.submit({
          url: u,
          kind: /^magnet:/i.test(u) || /\.torrent$/i.test(u) ? 'torrent' : 'auto',
          title: lines.length === 1 ? title.value : '',
          quality: quality.value,
          outDir: outDir.value || undefined,
          headers,
        });
      }
      url.value = ''; title.value = ''; probe.value = null;
      onDone();
    } catch (err) {
      error.value = (err as Error).message;
    } finally {
      busy.value = false;
    }
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    const files = Array.from(e.dataTransfer?.files || []);
    const paths = files.map((f) => window.vidhawk.pathForFile(f)).filter((p) => /\.torrent$/i.test(p));
    if (paths.length) url.value = [url.value, ...paths].filter(Boolean).join('\n');
    const text = e.dataTransfer?.getData('text/uri-list') || e.dataTransfer?.getData('text/plain');
    if (text && /^(https?:|magnet:)/i.test(text)) url.value = [url.value, text.trim()].filter(Boolean).join('\n');
  }

  const p = probe.value;
  return (
    <section class="page">
      <header class="page-head"><h1>Add download</h1></header>
      <form class="form" onSubmit={submit} onDragOver={(e) => e.preventDefault()} onDrop={onDrop}>
        <label class="field">
          <span>Links <small>one per line — http(s), magnet: or a .torrent path. Drop files here.</small></span>
          <textarea rows={4} value={url.value} onInput={(e) => { url.value = (e.target as HTMLTextAreaElement).value; probe.value = null; }} placeholder="https://… or magnet:?xt=urn:btih:…" spellcheck={false} required />
        </label>

        <div class="row">
          <Button icon="search" onClick={runProbe} disabled={!canProbe || probing.value || !connected.value}>{probing.value ? 'Inspecting…' : 'Inspect link'}</Button>
          {p && (
            <span class="probe-summary">
              <Icon name="check" /> {p.kind.toUpperCase()}
              {p.size ? ` · ${bytes(p.size)}` : ''}
              {p.variants?.length ? ` · ${p.variants.length} qualities` : ''}
              {p.segments ? ` · ${p.segments} segments${p.duration ? ` · ${duration(p.duration)}` : ''}` : ''}
              {p.drm ? ' · DRM protected — cannot download' : ''}
              {p.live ? ' · live' : ''}
            </span>
          )}
        </div>

        <div class="grid-2">
          <label class="field">
            <span>Title <small>optional</small></span>
            <input type="text" value={title.value} onInput={(e) => (title.value = (e.target as HTMLInputElement).value)} placeholder={isMagnet ? 'Taken from the torrent' : 'Taken from the page or file'} />
          </label>
          <label class="field">
            <span>Quality</span>
            <select value={quality.value} onChange={(e) => (quality.value = (e.target as HTMLSelectElement).value)}>
              <option value="best">Best available</option>
              {(p?.variants || []).map((v) => (
                <option key={v.label} value={String(v.height || v.label)}>{v.label}{v.width ? ` · ${v.width}×${v.height}` : ''}{v.bandwidth ? ` · ${Math.round(v.bandwidth / 1000)} kbps` : ''}</option>
              ))}
              {!p?.variants?.length && ['2160p', '1440p', '1080p', '720p', '480p', '360p'].map((q) => <option key={q} value={q}>{q} (cap)</option>)}
              <option value="worst">Smallest</option>
            </select>
          </label>
          <label class="field">
            <span>Referer <small>for protected streams</small></span>
            <input type="url" value={referer.value} onInput={(e) => (referer.value = (e.target as HTMLInputElement).value)} placeholder="https://site.example/page" />
          </label>
          <label class="field">
            <span>Save to</span>
            <div class="input-row">
              <input type="text" value={outDir.value} onInput={(e) => (outDir.value = (e.target as HTMLInputElement).value)} placeholder={config.value?.downloadDir || 'Default folder'} />
              <Button icon="folder" onClick={async () => { const d = await window.vidhawk.pickDirectory(outDir.value || config.value?.downloadDir); if (d) outDir.value = d; }}>Browse</Button>
            </div>
          </label>
        </div>

        {error.value && <p class="form-error"><Icon name="alert" /> {error.value}</p>}
        <div class="row end">
          <Button type="submit" variant="primary" icon="download" disabled={busy.value || !connected.value || !!p?.drm}>{busy.value ? 'Adding…' : 'Download'}</Button>
        </div>
      </form>
    </section>
  );
}
