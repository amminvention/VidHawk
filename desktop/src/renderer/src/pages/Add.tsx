import { useSignal } from '@preact/signals';
import type { JSX } from 'preact';
import { useEffect } from 'preact/hooks';
import { api, config, connected, type ProbeResult } from '../api';
import { bytes, duration } from '../format';
import { Button, Icon } from '../components/ui';
import { episodeName, parseEpisode, seasonFolder } from '../episode';

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
  const series = useSignal(false);
  const show = useSignal('');
  const season = useSignal('');
  const episode = useSignal('');
  const organize = useSignal(false);
  const detected = useSignal('');

  useEffect(() => { url.value = initialUrl; probe.value = null; }, [initialUrl]);

  function detectFrom(text: string) {
    const ep = parseEpisode(text);
    if (!ep) return;
    series.value = true;
    show.value = ep.show;
    season.value = ep.season == null ? '' : String(ep.season);
    episode.value = String(ep.episode);
    detected.value = `detected S${ep.season ?? '?'} E${ep.episode}`;
  }

  const sep = (config.value?.downloadDir || '').includes('\\') ? '\\' : '/';
  const joinPath = (...parts: string[]) => parts.filter(Boolean).map((p, i) => (i ? p.replace(/^[\\/]+/, '') : p.replace(/[\\/]+$/, ''))).join(sep);
  function finalTarget(): { title: string; outDir?: string } {
    let t = title.value;
    let dir = outDir.value || '';
    if (series.value && episode.value !== '') {
      const s = season.value === '' ? null : Number(season.value);
      t = episodeName(show.value, s, Number(episode.value));
      if (organize.value) dir = joinPath(dir || config.value?.downloadDir || '', ...seasonFolder(show.value, s));
    }
    return { title: t, outDir: dir || undefined };
  }

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
      detectFrom(title.value || probe.value.filename || url.value);
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
      const target = finalTarget();
      for (const u of lines) {
        const headers: Record<string, string> = {};
        if (referer.value) headers.referer = referer.value;
        await api.submit({
          url: u,
          kind: /^magnet:/i.test(u) || /\.torrent$/i.test(u) ? 'torrent' : 'auto',
          title: lines.length === 1 ? target.title : '',
          quality: quality.value,
          outDir: target.outDir,
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
            <input type="text" value={title.value} onInput={(e) => { title.value = (e.target as HTMLInputElement).value; detectFrom(title.value); }} placeholder={isMagnet ? 'Taken from the torrent' : 'Taken from the page or file'} />
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

        <div class="card series">
          <label class="check"><input type="checkbox" checked={series.value} onChange={(e) => (series.value = (e.target as HTMLInputElement).checked)} /> Series episode {detected.value && <small class="ok-text">{detected.value}</small>}</label>
          {series.value && (
            <div class="grid-3">
              <label class="field"><span>Show</span><input type="text" value={show.value} onInput={(e) => (show.value = (e.target as HTMLInputElement).value)} /></label>
              <label class="field"><span>Season</span><input type="number" min={0} max={99} value={season.value} onInput={(e) => (season.value = (e.target as HTMLInputElement).value)} /></label>
              <label class="field"><span>Episode</span><input type="number" min={0} max={999} value={episode.value} onInput={(e) => (episode.value = (e.target as HTMLInputElement).value)} /></label>
              <label class="check" style={{ gridColumn: '1 / -1' }}><input type="checkbox" checked={organize.value} onChange={(e) => (organize.value = (e.target as HTMLInputElement).checked)} /> File under <code>Show{sep}Season 01{sep}</code> — name becomes <code>{episodeName(show.value, season.value === '' ? null : Number(season.value), Number(episode.value || 0))}</code></label>
            </div>
          )}
        </div>

        {error.value && <p class="form-error"><Icon name="alert" /> {error.value}</p>}
        <div class="row end">
          <Button type="submit" variant="primary" icon="download" disabled={busy.value || !connected.value || !!p?.drm}>{busy.value ? 'Adding…' : 'Download'}</Button>
        </div>
      </form>
    </section>
  );
}
