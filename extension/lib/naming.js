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

  return { cleanTitle, pickTitle, sanitize, suggestName, fileNameFromUrl, stripExt };
})();

if (typeof module !== 'undefined') module.exports = VidHawkNaming;
