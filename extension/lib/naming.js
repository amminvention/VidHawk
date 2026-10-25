// Turns page metadata into a good file name. IDM uses document.title, which
// yields names like "Dar-e-nijaat - Detail Page.mp4"; we prefer structured
// metadata (JSON-LD, Open Graph) and strip site-name / boilerplate segments.
const VidHawkNaming = (() => {
  const GENERIC = /^(watch|watch online|watch free|detail page|details?|home|homepage|video|videos|player|stream|streaming|online|free|hd|full hd|official( site| website)?|untitled|download|episode|(?:an? )?(?:video|live|online) ?streaming (?:portal|platform|site|service|app)|\d+|[\s\W]*)$/i;
  const SEP = /\s+[-|–—:•·»«\/]+\s+/;
  // Site slogans that often ride along in <title>/og:title and never name the video.
  const BOILERPLATE = /\b(?:your |the )?(?:video |live |online )?streaming (?:guide|portal|platform|site|service|hub|app)\b|\bwatch (?:movies?|series|tv shows?|anime|online|free|now)\b|\bfree (?:movies?|streaming|download|online)\b|\bonline free\b|\bofficial (?:web)?site\b|\bhome ?page\b|\bstream (?:movies|now|online)\b|\b(?:movies|series) online\b/i;
  const EPISODE = /\bs\d{1,2}\s*[ -]?\s*e\d{1,3}\b|\bseason\s*\d|\bepisode\s*\d|\bep\.?\s*\d/i;
  const YEAR = /\b(?:19|20)\d{2}\b/;

  function siteWords(pageUrl) {
    try {
      const host = new URL(pageUrl).hostname.replace(/^www\./, '').toLowerCase();
      const parts = host.split('.');
      if (parts.length > 1) parts.pop();
      return parts.filter((p) => p.length > 2);
    } catch {
      return [];
    }
  }

  function cleanTitle(raw, pageUrl) {
    if (!raw) return '';
    const s = String(raw).replace(/^\(\d+\)\s*/, '').replace(/[▶►⏵]+/g, ' ').replace(/\s+/g, ' ').trim();
    if (!s) return '';
    const words = siteWords(pageUrl);
    const junk = (p) => {
      const q = p.trim().toLowerCase().replace(/\s+/g, ' ');
      if (!q || GENERIC.test(q) || BOILERPLATE.test(q)) return true;
      const compact = q.replace(/[\s._-]+/g, '');
      return words.some((w) => q === w || q === `${w}.com` || q === `${w} tv` || compact === w || compact === `${w}tv` || compact === `${w}com` || (compact.includes(w) && compact.length <= w.length + 8));
    };
    const parts = s.split(SEP);
    while (parts.length > 1 && junk(parts[parts.length - 1])) parts.pop();
    while (parts.length > 1 && junk(parts[0])) parts.shift();
    return parts.join(' - ').trim();
  }

  // Scores a cleaned candidate: distinctive words, year / season / episode
  // markers count for it; site slogans count against it. `rank` (0 = JSON-LD …
  // 4 = document.title) only breaks ties, so a specific <title> beats a
  // generic og:title.
  function score(c, rank) {
    const words = c.toLowerCase().match(/[\p{L}\p{N}]{3,}/gu) || [];
    let s = Math.min(words.length, 8);
    if (EPISODE.test(c)) s += 4;
    if (YEAR.test(c)) s += 1;
    if (BOILERPLATE.test(c)) s -= 6;
    return s + (5 - rank) * 0.1;
  }

  // meta: { ld, og, tw, h1, h2, title } (all optional strings)
  function pickTitle(meta, pageUrl) {
    meta = meta || {};
    const candidates = [meta.ld, meta.og, meta.tw, meta.h1, meta.h2, meta.title]
      .map((x, rank) => ({ text: cleanTitle(x, pageUrl), rank }))
      .filter((c) => c.text && c.text.length >= 2 && c.text.length <= 180 && !GENERIC.test(c.text));
    if (!candidates.length) return '';
    return candidates.sort((a, b) => score(b.text, b.rank) - score(a.text, a.rank))[0].text;
  }

  function sanitize(name) {
    const s = String(name || '')
      .replace(/[<>:"/\\|?*\u0000-\u001f]/g, '')
      .replace(/\s+/g, ' ')
      .replace(/[. ]+$/, '')
      .trim();
    return (s.length > 150 ? s.slice(0, 150) : s) || 'download';
  }

  function fileNameFromUrl(url) {
    try {
      const last = new URL(url).pathname.split('/').filter(Boolean).pop() || '';
      return decodeURIComponent(last);
    } catch {
      return '';
    }
  }

  function stripExt(name) {
    return String(name || '').replace(/\.[a-z0-9]{1,5}$/i, '');
  }

  // item: { kind, media, ext, filename, url }
  function suggestName(item, meta, pageUrl) {
    if (item.filename) return sanitize(item.filename);
    if (item.kind === 'torrent') return sanitize(fileNameFromUrl(item.url) || 'download.torrent');
    if (item.kind === 'file' && item.media === 'other') return sanitize(fileNameFromUrl(item.url) || 'download');
    const title = pickTitle(meta, pageUrl) || sanitize(stripExt(fileNameFromUrl(item.url))) || 'video';
    const ext = item.kind === 'hls' || item.kind === 'dash' ? 'mp4' : item.ext || (item.media === 'audio' ? 'm4a' : 'mp4');
    return `${sanitize(title)}.${ext}`;
  }

  // Season/episode detection: "S01E02", "S1 E1", "1x02", "Season 1 Episode 2", "Episode 2", "Ep. 2".
  const EP_PATTERNS = [
    { re: /\bS(\d{1,2})\s*[ .\-_]?\s*E(\d{1,3})\b/i, s: 1, e: 2 },
    { re: /\b(\d{1,2})x(\d{1,3})\b/i, s: 1, e: 2 },
    { re: /\bSeason\s*(\d{1,2})\b[^\d]{0,24}?\bEpisode\s*(\d{1,3})\b/i, s: 1, e: 2 },
    { re: /\bEpisode\s*(\d{1,3})\b/i, s: null, e: 1 },
    { re: /\bEp\.?\s*(\d{1,3})\b/i, s: null, e: 1 },
  ];
  const pad2 = (n) => String(n).padStart(2, '0');

  // Returns { show, season, episode } or null when the title carries no episode marker.
  function parseEpisode(title) {
    const t = String(title || '');
    for (const p of EP_PATTERNS) {
      const m = p.re.exec(t);
      if (!m) continue;
      const season = p.s ? Number(m[p.s]) : null;
      const episode = Number(m[p.e]);
      let show = t.slice(0, m.index).replace(/[\s\-–—:·•|(\[.]+$/, '').trim();
      if (!show) show = t.slice(m.index + m[0].length).replace(/^[\s\-–—:·•|)\]]+/, '').trim();
      show = show.replace(/\s*\((?:19|20)\d{2}\)\s*$/, '').trim();
      // Scene-style "Some.Show.S02E07" → "Some Show".
      if (!/\s/.test(show) && show.includes('.')) show = show.replace(/\./g, ' ').trim();
      return { show, season, episode };
    }
    return null;
  }

  function episodeName(show, season, episode) {
    const base = sanitize(show || 'Episode');
    const tag = season != null && season !== '' ? `S${pad2(season)}E${pad2(episode)}` : `E${pad2(episode)}`;
    return `${base} - ${tag}`;
  }

  function seasonFolder(show, season) {
    const parts = [sanitize(show || 'Show')];
    if (season != null && season !== '') parts.push(`Season ${pad2(season)}`);
    return parts;
  }

  return { cleanTitle, pickTitle, sanitize, suggestName, fileNameFromUrl, stripExt, parseEpisode, episodeName, seasonFolder };
})();

if (typeof module !== 'undefined') module.exports = VidHawkNaming;
