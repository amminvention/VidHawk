// Layer 3 (isolated world): media elements, page metadata, SPA navigation,
// and the relay for inject.js reports coming through window.postMessage.
(() => {
  if (window.__vidhawkContent) return;
  window.__vidhawkContent = true;

  const isTop = window.top === window;
  const send = (msg) => {
    try { chrome.runtime.sendMessage(msg, () => void chrome.runtime.lastError); } catch {}
  };

  window.addEventListener('message', (e) => {
    const d = e.data;
    if (e.source !== window || !d || d.__vidhawk__ !== 1 || !d.msg) return;
    send(d.msg);
  });

  const q = (sel) => document.querySelector(sel);
  const meta = (n) => q(`meta[property="${n}"], meta[name="${n}"]`)?.getAttribute('content')?.trim() || '';

  function ldTitle() {
    for (const s of document.querySelectorAll('script[type="application/ld+json"]')) {
      try {
        const j = JSON.parse(s.textContent);
        const list = Array.isArray(j) ? j : (j && j['@graph']) || [j];
        for (const o of list) {
          const type = String((o && o['@type']) || '');
          if (/VideoObject|Movie|TVEpisode|Episode|Clip|MusicVideo/.test(type) && o.name) return String(o.name);
        }
      } catch {}
    }
    return '';
  }

  let lastMeta = '';
  function playerState() {
    const els = [...document.querySelectorAll('video, audio')];
    const frames = document.querySelectorAll('iframe').length;
    return {
      players: els.length,
      playing: els.some((v) => !v.paused && !v.ended && v.readyState > 2),
      loaded: els.some((v) => v.readyState > 0 || !!(v.currentSrc || v.src)),
      frames,
    };
  }
  // Links on the page that look like other episodes/parts of the same series:
  // text or URL with S01E02 / 1x02 / Episode 3 / ep-4 markers, rel=next/prev,
  // or a URL that differs from the current one only by a number.
  const EP_TEXT = /\bS\d{1,2}\s*[ .\-_]?\s*E\d{1,3}\b|\b\d{1,2}x\d{1,3}\b|\bSeason\s*\d{1,2}\b|\bEpisode\s*\d{1,3}\b|\bEp\.?\s*\d{1,3}\b|\bPart\s*\d{1,3}\b/i;
  const EP_URL = /[\/\-_](?:s\d{1,2}e\d{1,3}|\d{1,2}x\d{1,3}|ep(?:isode)?[\-_]?\d{1,3}|season[\-_]?\d{1,2}[\/\-_]+(?:episode|ep)?[\-_]?\d{1,3}|e\d{1,3})(?=[\/\-_.?#]|$)/i;
  function relatedLinks() {
    if (!isTop) return [];
    const here = location.href.split('#')[0];
    const hereDigits = here.replace(/\d+/g, '#');
    const out = [];
    const seen = new Set([here]);
    for (const a of document.querySelectorAll('a[href]')) {
      let href;
      try { href = new URL(a.getAttribute('href'), location.href).href.split('#')[0]; } catch { continue; }
      if (!/^https?:/.test(href) || seen.has(href)) continue;
      const text = (a.innerText || a.getAttribute('title') || a.getAttribute('aria-label') || '').replace(/\s+/g, ' ').trim().slice(0, 120);
      const rel = (a.getAttribute('rel') || '').toLowerCase();
      const sameShape = href.replace(/\d+/g, '#') === hereDigits && /\d/.test(href);
      if (!(EP_TEXT.test(text) || EP_URL.test(href) || rel === 'next' || rel === 'prev' || sameShape)) continue;
      seen.add(href);
      out.push({ href, text, rel: rel || undefined });
      if (out.length >= 80) break;
    }
    return out;
  }
  function snapshotMeta() {
    return {
      related: relatedLinks(),
      title: document.title || '',
      og: meta('og:title'),
      tw: meta('twitter:title'),
      ld: ldTitle(),
      h1: (q('h1')?.innerText || '').trim().slice(0, 200),
      h2: (q('h2, [role="heading"]')?.innerText || '').trim().slice(0, 200),
      url: location.href,
      player: playerState(),
    };
  }
  function reportMeta() {
    if (!isTop) return;
    const m = snapshotMeta();
    const s = JSON.stringify(m);
    if (s === lastMeta) return;
    lastMeta = s;
    send({ type: 'meta', meta: m, pageUrl: location.href });
  }
  // The popup asks for a fresh snapshot on open: SPAs often set the title late.
  if (isTop) {
    chrome.runtime.onMessage.addListener((msg, _sender, sendResponse) => {
      if (msg?.type === 'get-meta') { sendResponse(snapshotMeta()); return true; }
      return false;
    });
  }

  const reported = new Set();
  function scanMedia() {
    for (const el of document.querySelectorAll('video, audio, source')) {
      const src = el.currentSrc || el.src || el.getAttribute('src') || '';
      if (!/^https?:/i.test(src) || reported.has(src)) continue;
      reported.add(src);
      const tag = el.tagName.toLowerCase() === 'source' ? (el.parentElement?.tagName || 'video').toLowerCase() : el.tagName.toLowerCase();
      send({ type: 'media-src', url: src, tag, pageUrl: location.href });
    }
  }

  let timer = null;
  function tick() {
    reportMeta();
    scanMedia();
  }
  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(tick, 400);
  }

  document.addEventListener('DOMContentLoaded', tick);
  window.addEventListener('load', tick);
  if (document.readyState !== 'loading') tick();

  const observe = () => {
    if (!document.documentElement) return;
    new MutationObserver(schedule).observe(document.documentElement, {
      childList: true, subtree: true, attributes: true, attributeFilter: ['src'],
    });
  };
  if (document.documentElement) observe(); else document.addEventListener('DOMContentLoaded', observe);

  let lastHref = location.href;
  setInterval(() => {
    if (location.href !== lastHref) {
      lastHref = location.href;
      lastMeta = '';
      setTimeout(tick, 600);
    } else if (document.visibilityState === 'visible') {
      scanMedia();
    }
  }, 2000);
})();
