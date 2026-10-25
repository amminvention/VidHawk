// Popup: shows what the three capture layers found on the active tab, probes
// streams for selectable qualities, and controls engine jobs.
(() => {
  const $ = (sel, root = document) => root.querySelector(sel);
  const bg = async (type, extra = {}) => {
    const r = await chrome.runtime.sendMessage({ type, ...extra });
    if (!r) throw new Error('no response from service worker');
    if (!r.ok) throw new Error(r.error || 'unknown error');
    return r.result;
  };

  let tabId = null;
  let tabUrl = '';
  let data = { tab: { items: {}, meta: {} }, jobs: {}, engine: {}, settings: {} };
  const probing = new Set();
  const names = new Map();
  const qualities = new Map();
  const options = new Map(); // item id -> { open, folder, series, show, season, episode, organize }
  let recentFolders = [];

  const defaultFolder = () => data.engine?.info?.downloadDir || '';
  const sep = () => (defaultFolder().includes('\\') ? '\\' : '/');
  const joinPath = (...parts) => parts.filter(Boolean).map((p, i) => (i ? p.replace(/^[\\/]+/, '') : p.replace(/[\\/]+$/, ''))).join(sep());

  async function loadRecentFolders() {
    try { recentFolders = (await chrome.storage.local.get('recentFolders')).recentFolders || []; } catch { recentFolders = []; }
    $('#recentFolders').replaceChildren(...recentFolders.map((f) => { const o = document.createElement('option'); o.value = f; return o; }));
  }
  async function rememberFolder(folder) {
    if (!folder) return;
    recentFolders = [folder, ...recentFolders.filter((f) => f !== folder)].slice(0, 8);
    try { await chrome.storage.local.set({ recentFolders }); } catch {}
  }

  // Resolves the item's options into the final title and output folder.
  function resolveTarget(item, rawName, opt) {
    let title = isStream(item) ? VidHawkNaming.stripExt(rawName) : rawName;
    let outDir = (opt?.folder || '').trim();
    if (opt?.series && opt.episode !== '' && opt.episode != null) {
      const ext = isStream(item) ? '' : (rawName.match(/\.[a-z0-9]{1,5}$/i) || [''])[0];
      title = VidHawkNaming.episodeName(opt.show, opt.season === '' ? null : opt.season, opt.episode) + ext;
      if (opt.organize) outDir = joinPath(outDir || defaultFolder(), ...VidHawkNaming.seasonFolder(opt.show, opt.season === '' ? null : opt.season));
    }
    return { title, outDir };
  }

  function wireOptions(node, item, nameInput) {
    const opt = options.get(item.id) || (() => {
      const guess = VidHawkNaming.parseEpisode(nameInput.value) || VidHawkNaming.parseEpisode(data.tab.meta?.h1 || '') || VidHawkNaming.parseEpisode(data.tab.meta?.title || '');
      const baseName = VidHawkNaming.stripExt(nameInput.value).replace(/\s\(\d+\)$/, '');
      const o = { open: false, folder: '', series: !!guess, show: guess?.show || baseName, season: guess?.season ?? '', episode: guess?.episode ?? '', organize: false, detected: !!guess };
      options.set(item.id, o);
      return o;
    })();
    const panel = $('[data-role="options"]', node);
    const toggle = $('[data-action="options"]', node);
    const fields = $('[data-role="seriesFields"]', node);
    const seriesBox = $('[data-role="series"]', node);
    const folder = $('[data-role="folder"]', node);
    const show = $('[data-role="show"]', node);
    const season = $('[data-role="season"]', node);
    const episode = $('[data-role="episode"]', node);
    const organize = $('[data-role="organize"]', node);
    panel.hidden = !opt.open;
    toggle.setAttribute('aria-expanded', String(opt.open));
    toggle.classList.toggle('on', opt.open || opt.series);
    folder.value = opt.folder;
    folder.placeholder = defaultFolder() || 'Default folder';
    seriesBox.checked = opt.series;
    $('[data-role="seriesHint"]', node).textContent = opt.detected ? `detected S${opt.season === '' ? '?' : opt.season} E${opt.episode}` : '';
    fields.hidden = !opt.series;
    show.value = opt.show;
    season.value = opt.season;
    episode.value = opt.episode;
    organize.checked = opt.organize;
    toggle.addEventListener('click', () => { opt.open = !opt.open; panel.hidden = !opt.open; toggle.setAttribute('aria-expanded', String(opt.open)); toggle.classList.toggle('on', opt.open || opt.series); });
    folder.addEventListener('input', () => { opt.folder = folder.value; });
    seriesBox.addEventListener('change', () => { opt.series = seriesBox.checked; fields.hidden = !opt.series; toggle.classList.toggle('on', opt.open || opt.series); });
    show.addEventListener('input', () => { opt.show = show.value; });
    season.addEventListener('input', () => { opt.season = season.value === '' ? '' : Number(season.value); });
    episode.addEventListener('input', () => { opt.episode = episode.value === '' ? '' : Number(episode.value); });
    organize.addEventListener('change', () => { opt.organize = organize.checked; });
    return opt;
  }

  const mediaList = $('#mediaList');
  const staleList = $('#staleList');
  const jobList = $('#jobList');
  const mediaTpl = $('#mediaItemTpl');
  const jobTpl = $('#jobItemTpl');

  // ---------------------------------------------------------------- helpers
  const fmtBytes = (n) => {
    if (n == null || isNaN(n) || n <= 0) return '';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    let v = Number(n);
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return `${v.toFixed(v < 10 && i ? 1 : 0)} ${u[i]}`;
  };
  const fmtDuration = (s) => {
    if (!s || !isFinite(s)) return '';
    s = Math.round(s);
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    return h ? `${h}h ${m}m` : m ? `${m}m ${sec}s` : `${sec}s`;
  };
  const host = (u) => { try { return new URL(u).hostname.replace(/^www\./, ''); } catch { return ''; } };
  const kindLabel = (item) => {
    if (item.kind === 'hls') return 'HLS';
    if (item.kind === 'dash') return 'DASH';
    if (item.kind === 'torrent') return 'TORRENT';
    return (item.ext || item.media || 'file').toUpperCase().slice(0, 7);
  };
  const isStream = (item) => item.kind === 'hls' || item.kind === 'dash';
  const toast = (msg) => {
    let el = $('.toast');
    if (!el) { el = document.createElement('div'); el.className = 'toast'; document.body.appendChild(el); }
    el.textContent = msg;
    el.classList.add('show');
    clearTimeout(toast.t);
    toast.t = setTimeout(() => el.classList.remove('show'), 1800);
  };

  const SHOW_LIMIT = 5;
  let showAll = false;

  function visibleItems(tab) {
    const items = Object.values(tab.items || {}).filter((i) => i.kind !== 'segment' && !i.hidden);
    const keys = new Set(Object.keys(tab.items || {}));
    return items
      .filter((i) => !(i.childOf && keys.has(i.childOf)))
      .sort((a, b) => (b.playing || 0) - (a.playing || 0)
        || Number(isStream(b)) - Number(isStream(a))
        || Number(b.verified) - Number(a.verified)
        || (b.seen || 0) - (a.seen || 0)
        || b.added - a.added);
  }

  // Several streams on one page often share a title; number the duplicates.
  // Recomputed on every render from the currently visible items, so numbering
  // disappears again once variant playlists collapse under their master.
  // `names` only holds names the user edited by hand.
  const suggested = new Map();
  function uniqueNames(items) {
    const used = new Map();
    for (const item of items) {
      const base = VidHawkNaming.suggestName(item, data.tab.meta, tabUrl);
      const n = (used.get(base) || 0) + 1;
      used.set(base, n);
      suggested.set(item.id, n === 1 ? base : base.replace(/(\.[a-z0-9]{1,5})?$/i, (ext) => ` (${n})${ext}`));
    }
  }

  // ---------------------------------------------------------------- engine
  function renderEngine() {
    const e = data.engine || {};
    const df = $('#defaultFolder');
    if (document.activeElement !== df) df.value = e.info?.downloadDir || '';
    const btn = $('#engineBtn');
    btn.classList.toggle('ok', !!e.connected);
    btn.classList.toggle('err', !e.connected && !!e.error);
    $('#engineText').textContent = e.connected ? `Engine v${e.info?.version || '?'}` : e.error ? 'Engine offline' : 'Connecting…';
    $('#engineBanner').hidden = e.connected || !e.error;
    $('#engineError').textContent = e.error || '';
    for (const b of document.querySelectorAll('[data-action="download"], #addForm button, #pageYtdlp')) b.disabled = !e.connected;
  }

  // ----------------------------------------------------------------- media
  function renderMedia() {
    const tab = data.tab || { items: {} };
    const all = visibleItems(tab);
    const live = all.filter((i) => !i.stale);
    const stale = all.filter((i) => i.stale);
    uniqueNames(live);
    uniqueNames(stale);
    const shown = showAll ? live : live.slice(0, SHOW_LIMIT);
    mediaList.replaceChildren(...shown.map(renderItem));
    if (live.length > SHOW_LIMIT) {
      const more = document.createElement('li');
      more.className = 'more';
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'link';
      btn.textContent = showAll ? 'Show fewer' : `Show all ${live.length} streams on this page`;
      btn.addEventListener('click', () => { showAll = !showAll; renderMedia(); });
      more.appendChild(btn);
      mediaList.appendChild(more);
    }
    staleList.replaceChildren(...stale.map(renderItem));
    $('#staleWrap').hidden = stale.length === 0;
    $('#mediaEmpty').hidden = live.length > 0 || stale.length > 0;
    $('#mediaCount').textContent = live.length ? String(live.length) : '';
    const player = tab.meta?.player;
    const emptyText = $('#mediaEmpty p');
    if (emptyText) {
      if (player?.players > 0 && !player.loaded) emptyText.textContent = 'A video player is on this page but has not started. Press Play, then reopen VidHawk — the stream is caught the moment the player requests it.';
      else if (player?.players > 0) emptyText.textContent = 'The player is active but its media has not been requested yet. Let it play a few seconds, then reopen VidHawk.';
      else if (player?.frames > 0) emptyText.textContent = 'No media caught yet. The video may live inside an embedded frame — press Play on it, then reopen VidHawk.';
      else emptyText.textContent = 'Nothing caught yet. Play the video, or reload the page with VidHawk open.';
    }
    const seg = tab.segments;
    const note = $('#segmentsNote');
    if (seg && seg.count >= 3 && !live.some(isStream)) {
      note.hidden = false;
      note.textContent = `${seg.count} media segments (${(seg.ext || '').toUpperCase()}) seen but no playlist caught yet — keep playing, reload the page, or use "Grab page via yt-dlp".`;
    } else {
      note.hidden = true;
    }
    autoProbe(live);
  }

  function renderItem(item) {
    const node = mediaTpl.content.firstElementChild.cloneNode(true);
    node.dataset.key = item.id;
    const kind = $('[data-role="kind"]', node);
    kind.textContent = kindLabel(item);
    kind.className = `kind ${item.kind}${item.verified === false ? ' api' : ''}`;
    kind.title = item.source === 'api' ? 'Found inside an API response (unverified)' : `Detected via ${item.source || 'network'}`;

    const name = $('[data-role="name"]', node);
    name.value = names.get(item.id) ?? suggested.get(item.id) ?? VidHawkNaming.suggestName(item, data.tab.meta, tabUrl);
    name.addEventListener('input', () => names.set(item.id, name.value));

    const meta = $('[data-role="meta"]', node);
    const parts = [];
    if (item.playing) parts.push('<span class="ok">Playing now</span><span class="sep"></span>');
    parts.push(`<span class="host" title="${escapeAttr(item.url)}">${escapeHtml(host(item.url))}</span>`);
    if (item.size) parts.push(`<span class="sep"></span><span>${fmtBytes(item.size)}</span>`);
    const p = item.probe;
    if (p) {
      if (p.drm) parts.push(`<span class="sep"></span><span class="warn">DRM protected</span>`);
      else if (p.kind === 'hls' && !p.master && p.segments) parts.push(`<span class="sep"></span><span>${p.segments} segments${p.duration ? ` · ${fmtDuration(p.duration)}` : ''}${p.live ? ' · live' : ''}</span>`);
      else if (p.variants?.length) parts.push(`<span class="sep"></span><span class="ok">${p.variants.length} qualities</span>`);
      else if (p.kind === 'file' && p.size) parts.push(`<span class="sep"></span><span>${fmtBytes(p.size)}</span>`);
      if (p.audio?.length > 1) parts.push(`<span class="sep"></span><span>${p.audio.length} audio tracks</span>`);
    }
    if (item.seen > 1) parts.push(`<span class="sep"></span><span>seen ×${item.seen}</span>`);
    meta.innerHTML = parts.join('');

    const quality = $('[data-role="quality"]', node);
    const variants = p?.variants || [];
    if (variants.length) {
      quality.hidden = false;
      quality.replaceChildren(
        option('best', `Best (${variants[0].label || 'auto'})`),
        ...variants.map((v) => option(String(v.height || v.label), `${v.label}${v.width ? ` · ${v.width}×${v.height}` : ''}${v.bandwidth ? ` · ${Math.round(v.bandwidth / 1000)} kbps` : ''}`)),
      );
      quality.value = qualities.get(item.id) || 'best';
      quality.addEventListener('change', () => qualities.set(item.id, quality.value));
    }
    const probe = $('[data-role="probe"]', node);
    probe.hidden = !probing.has(item.id);
    probe.textContent = 'Reading stream…';

    const opt = wireOptions(node, item, name);
    const dl = $('[data-action="download"]', node);
    dl.disabled = !data.engine.connected || !!p?.drm;
    dl.addEventListener('click', () => download(item, name.value, quality.hidden ? 'best' : quality.value, opt));
    $('[data-action="copy"]', node).addEventListener('click', async () => { await navigator.clipboard.writeText(item.url); toast('URL copied'); });
    $('[data-action="remove"]', node).addEventListener('click', async () => { await bg('remove-item', { tabId, key: item.id }); refresh(); });
    return node;
  }

  function option(value, label) {
    const o = document.createElement('option');
    o.value = value;
    o.textContent = label;
    return o;
  }
  const escapeHtml = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const escapeAttr = escapeHtml;

  async function autoProbe(items) {
    if (!data.engine.connected) return;
    const todo = items.filter((i) => (isStream(i) || i.verified === false) && !i.probe && !probing.has(i.id)).slice(0, 4);
    for (const item of todo) {
      probing.add(item.id);
      bg('probe', { url: item.url, headers: item.headers || {}, tabId })
        .catch((e) => { item.probeError = e.message; })
        .finally(() => { probing.delete(item.id); refresh(); });
    }
    if (todo.length) renderMediaOnly();
  }
  function renderMediaOnly() {
    for (const node of mediaList.children) {
      const probe = $('[data-role="probe"]', node);
      if (probe) probe.hidden = !probing.has(node.dataset.key);
    }
  }

  async function download(item, name, quality, opt) {
    const { title, outDir } = resolveTarget(item, name, opt);
    try {
      await bg('download', { spec: { url: item.url, kind: item.kind === 'file' && item.verified === false ? 'auto' : item.kind, title, quality: quality || 'best', headers: item.headers || {}, pageUrl: tabUrl, expectedSize: item.kind === 'file' ? item.size || 0 : 0, outDir: outDir || undefined } });
      if (opt?.folder) rememberFolder(opt.folder.trim());
      toast(outDir ? `Added → ${outDir}` : 'Added to VidHawk');
      refresh();
    } catch (e) {
      toast(`Failed: ${e.message}`);
    }
  }

  // --------------------------------------------- browser downloads (tab-independent)
  function renderRecent() {
    const recent = (data.recentDownloads || []).filter((d) => !d.takenOver);
    const section = $('#recentSection');
    section.hidden = recent.length === 0;
    $('#recentCount').textContent = recent.length ? String(recent.length) : '';
    const list = $('#recentList');
    list.replaceChildren(...recent.map((d) => {
      const node = mediaTpl.content.firstElementChild.cloneNode(true);
      const ext = VidHawkNaming.fileNameFromUrl(d.filename || '').split('.').pop();
      const kind = $('[data-role="kind"]', node);
      kind.textContent = (ext && ext.length <= 5 ? ext : (d.mime || 'file').split('/').pop()).toUpperCase().slice(0, 7);
      kind.className = 'kind';
      kind.title = 'Started by the browser (Download button or link)';
      const name = $('[data-role="name"]', node);
      name.value = d.filename || VidHawkNaming.fileNameFromUrl(d.url) || 'download';
      const meta = $('[data-role="meta"]', node);
      meta.innerHTML = `<span class="host" title="${escapeAttr(d.url)}">${escapeHtml(host(d.url))}</span>${d.size ? `<span class="sep"></span><span>${fmtBytes(d.size)}</span>` : ''}${d.mime ? `<span class="sep"></span><span>${escapeHtml(d.mime)}</span>` : ''}`;
      $('[data-role="quality"]', node).hidden = true;
      $('[data-role="probe"]', node).hidden = true;
      const dl = $('[data-action="download"]', node);
      dl.disabled = !data.engine.connected;
      dl.addEventListener('click', async () => {
        try {
          await bg('download', { spec: { url: d.url, kind: 'file', title: name.value, quality: 'best', headers: d.headers || {}, pageUrl: d.referrer || tabUrl, expectedSize: d.size || 0 } });
          await bg('forget-download', { url: d.url });
          toast('Added to VidHawk');
          refresh();
        } catch (e) { toast(`Failed: ${e.message}`); }
      });
      $('[data-action="copy"]', node).addEventListener('click', async () => { await navigator.clipboard.writeText(d.url); toast('URL copied'); });
      $('[data-action="remove"]', node).addEventListener('click', async () => { await bg('forget-download', { url: d.url }); refresh(); });
      return node;
    }));
  }

  // ------------------------------------------------------------------ jobs
  function renderJobs() {
    const jobs = Object.values(data.jobs || {}).sort((a, b) => {
      const act = (j) => (j.status === 'running' || j.status === 'queued' ? 0 : 1);
      return act(a) - act(b) || b.created - a.created;
    });
    jobList.replaceChildren(...jobs.slice(0, 30).map(renderJob));
    $('#jobsEmpty').hidden = jobs.length > 0;
    $('#clearDone').hidden = !jobs.some((j) => j.status !== 'running' && j.status !== 'queued');
  }

  function renderJob(j) {
    const node = jobTpl.content.firstElementChild.cloneNode(true);
    node.classList.add(j.status);
    const status = $('[data-role="status"]', node);
    status.textContent = j.status;
    status.className = `status ${j.status}`;
    const title = $('[data-role="title"]', node);
    title.textContent = j.title || j.url;
    title.title = j.path || j.url;

    const bar = $('[data-role="bar"]', node);
    const pct = j.total > 0 ? Math.min(100, j.percent || (j.done / j.total) * 100) : null;
    if (pct == null && j.status === 'running') node.classList.add('indeterminate');
    bar.setAttribute('aria-valuenow', pct == null ? 0 : Math.round(pct));
    bar.setAttribute('aria-label', `${j.title || 'download'} progress`);
    $('[data-role="fill"]', node).style.width = `${pct ?? (j.status === 'done' ? 100 : 0)}%`;

    const bits = [];
    if (pct != null) bits.push(`${pct.toFixed(pct < 10 ? 1 : 0)}%`);
    if (j.unit === 'segments' && j.total) bits.push(`${j.done}/${j.total} seg`);
    const sizeTotal = j.unit === 'bytes' ? j.total : j.estimate;
    if (j.bytes) bits.push(fmtBytes(j.bytes) + (sizeTotal ? ` of ${j.unit === 'bytes' ? '' : '≈'}${fmtBytes(sizeTotal)}` : ''));
    else if (sizeTotal) bits.push(`${j.unit === 'bytes' ? '' : '≈'}${fmtBytes(sizeTotal)}`);
    if (j.status === 'running' && j.speed > 0) {
      bits.push(`${fmtBytes(j.speed)}/s`);
      if (sizeTotal > j.bytes) bits.push(`${fmtDuration((sizeTotal - j.bytes) / j.speed)} left`);
    }
    if (j.peers) bits.push(`${j.peers} peers`);
    if (j.detail) bits.push(j.detail);
    if (j.status === 'done' && !bits.length) bits.push('complete');
    $('[data-role="meta"]', node).textContent = bits.join(' · ');

    const active = j.status === 'running' || j.status === 'queued';
    $('[data-action="pause"]', node).hidden = !active || j.kind === 'torrent' && false;
    $('[data-action="resume"]', node).hidden = !(j.status === 'paused' || j.status === 'error' || j.status === 'cancelled');
    $('[data-action="cancel"]', node).hidden = !active;
    $('[data-action="open"]', node).hidden = !(j.status === 'done' && j.path);
    $('[data-action="remove"]', node).hidden = active;
    $('[data-action="remove"]', node).classList.add('danger');
    for (const action of ['pause', 'resume', 'cancel']) {
      $(`[data-action="${action}"]`, node).addEventListener('click', async () => {
        try { await bg('job-action', { action, jobId: j.id }); } catch (e) { toast(e.message); }
        refresh();
      });
    }
    const confirm = $('[data-role="confirm"]', node);
    $('[data-action="remove"]', node).addEventListener('click', () => {
      // Nothing on disk yet (or a failed job): a plain remove is enough.
      if (!j.path) { bg('job-action', { action: 'remove', jobId: j.id }).then(refresh, (e) => toast(e.message)); return; }
      confirm.hidden = !confirm.hidden;
    });
    $('[data-action="remove-list"]', node).addEventListener('click', async () => {
      try { await bg('job-action', { action: 'remove', jobId: j.id }); } catch (e) { toast(e.message); }
      refresh();
    });
    $('[data-action="remove-file"]', node).addEventListener('click', async () => {
      try { await bg('job-action', { action: 'remove', jobId: j.id, deleteFile: true }); toast('File deleted'); } catch (e) { toast(e.message); }
      refresh();
    });
    $('[data-action="open"]', node).addEventListener('click', () => bg('open', { path: j.path }).catch((e) => toast(e.message)));
    const err = $('[data-role="error"]', node);
    err.hidden = !j.error;
    err.textContent = friendlyError(j);
    return node;
  }

  function friendlyError(j) {
    const e = j.error || '';
    if (!e) return '';
    if (/Unsupported URL/i.test(e)) return 'yt-dlp has no extractor for this site. Press Play on the page instead — VidHawk catches the stream directly once the player requests it.';
    if (/DRM|SAMPLE-AES|ContentProtection/i.test(e)) return 'This stream is DRM-protected and cannot be downloaded.';
    if (/HTTP 403/i.test(e)) return 'The server refused the request (403). The link may be bound to your browser session — try downloading from the page while it is open.';
    if (/HTTP 404|HTTP 410/i.test(e)) return 'The link has expired or no longer exists (404). Reload the page and catch it again.';
    if (/yt-dlp is not installed/i.test(e)) return 'yt-dlp is not installed. Install it (pip install yt-dlp) to use page downloads.';
    return e.replace(/^yt-dlp failed: exit status \d+: /, 'yt-dlp: ').slice(0, 400);
  }

  // ---------------------------------------------------------------- wiring
  async function refresh() {
    data = await bg('get-tab', { tabId });
    renderEngine();
    renderMedia();
    renderRecent();
    renderJobs();
    $('#takeOver').checked = !!data.settings?.takeOver;
  }

  $('#engineBtn').addEventListener('click', async () => {
    $('#engineText').textContent = 'Connecting…';
    data.engine = await bg('ping');
    renderEngine();
    renderMedia();
  });
  $('#addForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const url = $('#addUrl').value.trim();
    if (!url) return;
    const kind = url.startsWith('magnet:') ? 'torrent' : 'auto';
    try {
      await bg('download', { spec: { url, kind, title: '', quality: 'best', headers: { referer: tabUrl }, pageUrl: tabUrl } });
      $('#addUrl').value = '';
      toast('Added to VidHawk');
      refresh();
    } catch (err) {
      toast(`Failed: ${err.message}`);
    }
  });
  $('#pageYtdlp').addEventListener('click', async () => {
    try {
      await bg('download', { spec: { url: tabUrl, kind: 'ytdlp', title: '', quality: 'best', headers: {}, pageUrl: tabUrl } });
      toast('Page handed to yt-dlp');
      refresh();
    } catch (e) {
      toast(`Failed: ${e.message}`);
    }
  });
  $('#clearDone').addEventListener('click', async () => { await bg('clear-finished'); refresh(); });
  const dfInput = $('#defaultFolder');
  const dfSave = $('#saveFolder');
  dfInput.addEventListener('input', () => { dfSave.hidden = dfInput.value.trim() === (data.engine?.info?.downloadDir || ''); });
  dfSave.addEventListener('click', async () => {
    try {
      await bg('set-config', { set: { downloadDir: dfInput.value.trim() } });
      toast('Default folder saved');
      dfSave.hidden = true;
      data.engine = await bg('ping');
      renderEngine();
    } catch (e) { toast(`Failed: ${e.message}`); }
  });
  $('#takeOver').addEventListener('change', (e) => bg('settings', { settings: { takeOver: e.target.checked } }));

  chrome.runtime.onMessage.addListener((msg) => {
    if (msg?.type === 'state-changed') {
      clearTimeout(refresh.t);
      refresh.t = setTimeout(() => refresh().catch(() => {}), 120);
    }
  });

  (async () => {
    // ?tab=<id> lets the popup be opened as a normal tab for debugging/screenshots.
    const debugTab = Number(new URLSearchParams(location.search).get('tab'));
    const tab = debugTab ? await chrome.tabs.get(debugTab) : (await chrome.tabs.query({ active: true, currentWindow: true }))[0];
    tabId = tab?.id ?? null;
    tabUrl = tab?.url || '';
    await loadRecentFolders();
    await refresh();
    if (!data.engine.connected || Date.now() - (data.engine.lastPing || 0) > 30000) {
      data.engine = await bg('ping');
      renderEngine();
      renderMedia();
    }
  })().catch((e) => {
    $('#engineError').textContent = e.message;
    $('#engineBanner').hidden = false;
  });
})();
