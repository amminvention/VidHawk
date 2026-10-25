// Layer 2 (MAIN world): wraps fetch and XMLHttpRequest so manifests are
// recognised by their *body* (#EXTM3U / <MPD>) no matter the MIME type or
// status code, and so stream URLs embedded in JSON API responses are caught
// before the player even requests them.
(() => {
  if (window.__vidhawkInject) return;
  window.__vidhawkInject = true;

  const post = (msg) => {
    try { window.postMessage({ __vidhawk__: 1, msg }, '*'); } catch {}
  };
  const abs = (u) => {
    try { return new URL(u, location.href).href; } catch { return String(u); }
  };

  const MANIFEST_URL = /\.(m3u8|mpd)(\?|#|$)/i;
  const MEDIA_IN_TEXT = /https?:\/\/[^\s"'\\<>]+?\.(?:m3u8|mpd|mp4|webm|m4v|m4a|mp3|mov)(?:\?[^\s"'\\<>]*)?/gi;
  const ESCAPED_MEDIA = /https?:\\\/\\\/[^\s"'<>]+?\.(?:m3u8|mpd|mp4|webm|m4a|mp3)(?:\?[^\s"'<>]*)?/gi;
  const MAX_TEXT = 3 * 1024 * 1024;
  const seen = new Set();

  function reportManifest(url, text) {
    url = abs(url);
    if (seen.has(url)) return;
    seen.add(url);
    const head = text.slice(0, 200000);
    if (head.trimStart().startsWith('#EXTM3U')) {
      const master = /#EXT-X-STREAM-INF/.test(head);
      const children = [];
      if (master) {
        const lines = head.split(/\r?\n/);
        for (let i = 0; i < lines.length; i++) {
          const line = lines[i].trim();
          if (line.startsWith('#EXT-X-STREAM-INF')) {
            for (let j = i + 1; j < lines.length; j++) {
              const next = lines[j].trim();
              if (next && !next.startsWith('#')) { children.push(new URL(next, url).href); break; }
            }
          } else if (line.startsWith('#EXT-X-MEDIA:')) {
            const m = /URI="([^"]+)"/.exec(line);
            if (m) children.push(new URL(m[1], url).href);
          }
        }
      }
      post({ type: 'manifest', url, kind: 'hls', master, children, pageUrl: location.href });
    } else if (/<MPD[\s>]/i.test(head)) {
      post({ type: 'manifest', url, kind: 'dash', master: true, children: [], pageUrl: location.href });
    }
  }

  function scanText(url, text) {
    if (text.length > MAX_TEXT) return;
    const urls = new Set();
    let m;
    MEDIA_IN_TEXT.lastIndex = 0;
    while ((m = MEDIA_IN_TEXT.exec(text)) && urls.size < 20) urls.add(m[0]);
    ESCAPED_MEDIA.lastIndex = 0;
    while ((m = ESCAPED_MEDIA.exec(text)) && urls.size < 20) urls.add(m[0].replace(/\\\//g, '/'));
    const fresh = [...urls].filter((u) => !seen.has('api:' + u));
    if (!fresh.length) return;
    fresh.forEach((u) => seen.add('api:' + u));
    post({ type: 'api-media', urls: fresh, apiUrl: abs(url), pageUrl: location.href });
  }

  function textLike(ct, url, len) {
    ct = (ct || '').toLowerCase();
    if (/^(video|audio|image|font)\//.test(ct)) return false;
    if (/mpegurl|dash\+xml|json|javascript|xml|text\//.test(ct)) return true;
    if (!ct || /octet-stream/.test(ct)) return MANIFEST_URL.test(url) || (len > 0 && len < 512 * 1024);
    return false;
  }

  function inspect(url, ct, text) {
    if (typeof text !== 'string' || !text) return;
    const u = String(url || '');
    const head = text.slice(0, 64).trimStart();
    if (MANIFEST_URL.test(u) || /mpegurl|dash\+xml/i.test(ct || '') || head.startsWith('#EXTM3U') || head.startsWith('<MPD') ||
        (head.startsWith('<?xml') && /<MPD[\s>]/.test(text.slice(0, 4000)))) {
      reportManifest(u, text);
      return;
    }
    if (head.startsWith('{') || head.startsWith('[') || /json|javascript|text\/plain/i.test(ct || '')) scanText(u, text);
  }

  // ---- fetch
  const origFetch = window.fetch;
  if (typeof origFetch === 'function') {
    window.fetch = function (input, init) {
      const p = origFetch.apply(this, arguments);
      try {
        p.then((res) => {
          try {
            if (!res || !res.ok || res.type === 'opaque') return;
            const ct = res.headers.get('content-type') || '';
            const len = Number(res.headers.get('content-length')) || 0;
            const url = res.url || (typeof input === 'string' ? input : input && input.url) || '';
            if (!textLike(ct, url, len) || len > MAX_TEXT) return;
            res.clone().text().then((t) => inspect(url, ct, t)).catch(() => {});
          } catch {}
        }).catch(() => {});
      } catch {}
      return p;
    };
  }

  // ---- XMLHttpRequest
  const XO = XMLHttpRequest.prototype.open;
  const XS = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (method, url) {
    try { this.__vhUrl = String(url); } catch {}
    return XO.apply(this, arguments);
  };
  XMLHttpRequest.prototype.send = function () {
    try {
      this.addEventListener('load', () => {
        try {
          if (this.status < 200 || this.status >= 300) return;
          const ct = this.getResponseHeader('content-type') || '';
          const url = this.responseURL || this.__vhUrl || '';
          const rt = this.responseType;
          let text = null;
          if (rt === '' || rt === 'text') {
            if (textLike(ct, url, this.responseText.length)) text = this.responseText;
          } else if (rt === 'arraybuffer' && this.response && this.response.byteLength < MAX_TEXT && textLike(ct, url, this.response.byteLength)) {
            text = new TextDecoder().decode(new Uint8Array(this.response, 0, Math.min(this.response.byteLength, 300000)));
          } else if (rt === 'json' && this.response && typeof this.response === 'object') {
            text = JSON.stringify(this.response);
          }
          if (text) inspect(url, ct, text);
        } catch {}
      });
    } catch {}
    return XS.apply(this, arguments);
  };
})();
