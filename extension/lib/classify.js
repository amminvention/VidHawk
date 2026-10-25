// Classifies an HTTP response (observed via chrome.webRequest) into something
// VidHawk can download. Pure functions: shared by the service worker
// (importScripts) and the Node test runner (require).
const VidHawkClassify = (() => {
  const VIDEO = new Set(['mp4', 'm4v', 'webm', 'mkv', 'mov', 'avi', 'flv', 'wmv', 'mpg', 'mpeg', '3gp', 'ogv']);
  const AUDIO = new Set(['mp3', 'm4a', 'aac', 'ogg', 'oga', 'opus', 'flac', 'wav', 'wma']);
  const ARCHIVE = new Set(['zip', 'rar', '7z', 'tar', 'gz', 'bz2', 'xz', 'iso', 'img', 'exe', 'msi', 'dmg', 'pkg', 'deb', 'rpm', 'apk', 'pdf']);
  const SEGMENT_EXT = new Set(['ts', 'm4s', 'm4f', 'f4f', 'cmfv', 'cmfa', 'init', 'm4i']);
  const SEGMENT_MIME = new Set(['video/mp2t', 'video/iso.segment', 'application/x-mpegts']);
  const HLS_MIME = new Set([
    'application/vnd.apple.mpegurl', 'application/x-mpegurl', 'application/mpegurl',
    'audio/mpegurl', 'audio/x-mpegurl', 'video/mpegurl', 'video/x-mpegurl',
  ]);
  const DASH_MIME = new Set(['application/dash+xml', 'video/vnd.mpeg.dash.mpd']);
  const MIME_EXT = {
    'video/mp4': 'mp4', 'video/webm': 'webm', 'video/x-matroska': 'mkv', 'video/quicktime': 'mov',
    'video/x-msvideo': 'avi', 'video/x-flv': 'flv', 'video/mpeg': 'mpg', 'video/3gpp': '3gp', 'video/ogg': 'ogv',
    'audio/mpeg': 'mp3', 'audio/mp4': 'm4a', 'audio/aac': 'aac', 'audio/ogg': 'ogg', 'audio/wav': 'wav',
    'audio/x-wav': 'wav', 'audio/webm': 'webm', 'audio/flac': 'flac', 'audio/opus': 'opus',
    'application/zip': 'zip', 'application/x-zip-compressed': 'zip', 'application/pdf': 'pdf',
    'application/x-rar-compressed': 'rar', 'application/vnd.rar': 'rar', 'application/x-7z-compressed': '7z',
    'application/x-msdownload': 'exe', 'application/x-msi': 'msi', 'application/vnd.android.package-archive': 'apk',
    'application/x-bittorrent': 'torrent',
  };
  const MIN_MEDIA_BYTES = 512 * 1024;
  // Page assets are never user downloads; Google's JS CDN even serves scripts as attachment; filename="f.txt".
  const SKIP_TYPES = new Set(['script', 'stylesheet', 'font', 'image', 'ping', 'csp_report', 'xslt', 'web_manifest', 'beacon', 'speculative']);
  const SKIP_EXT = new Set(['js', 'mjs', 'css', 'html', 'htm', 'json', 'xml', 'svg', 'gif', 'png', 'jpg', 'jpeg', 'webp', 'avif', 'ico', 'woff', 'woff2', 'ttf', 'otf', 'map', 'txt', 'vtt', 'srt']);

  function urlExt(url) {
    try {
      const m = /\.([a-z0-9]{1,5})$/i.exec(new URL(url, 'http://x/').pathname);
      return m ? m[1].toLowerCase() : '';
    } catch {
      return '';
    }
  }

  function mimeBase(ct) {
    return (ct || '').split(';')[0].trim().toLowerCase();
  }

  function parseDisposition(h) {
    const out = { attachment: false, filename: null };
    if (!h) return out;
    out.attachment = /^\s*attachment/i.test(h);
    const star = /filename\*\s*=\s*(?:utf-8|utf8)''([^;]+)/i.exec(h);
    if (star) {
      try { out.filename = decodeURIComponent(star[1].trim()); } catch { out.filename = star[1].trim(); }
    } else {
      const plain = /filename\s*=\s*("([^"]*)"|'([^']*)'|([^;]+))/i.exec(h);
      if (plain) out.filename = (plain[2] ?? plain[3] ?? plain[4] ?? '').trim();
    }
    if (out.filename) out.filename = out.filename.replace(/[\\/]+/g, '_');
    return out;
  }

  function totalBytes(contentRange, contentLength) {
    if (contentRange) {
      const m = /\/(\d+)\s*$/.exec(contentRange);
      if (m) return Number(m[1]);
    }
    if (contentLength && /^\d+$/.test(String(contentLength))) return Number(contentLength);
    return null;
  }

  // r: { url, resourceType, statusCode, mime, contentLength, contentDisposition, contentRange }
  function classify(r) {
    if (r.statusCode !== 200 && r.statusCode !== 206) return null;
    if (SKIP_TYPES.has(r.resourceType)) return null;
    const ext = urlExt(r.url);
    const mime = mimeBase(r.mime);
    const cd = parseDisposition(r.contentDisposition);
    const cdExt = cd.filename ? urlExt(cd.filename) : '';
    const size = totalBytes(r.contentRange, r.contentLength);

    if (HLS_MIME.has(mime) || ext === 'm3u8' || cdExt === 'm3u8') return { kind: 'hls', ext: 'm3u8', size };
    if (DASH_MIME.has(mime) || ext === 'mpd' || cdExt === 'mpd') return { kind: 'dash', ext: 'mpd', size };
    if (ext === 'torrent' || cdExt === 'torrent' || mime === 'application/x-bittorrent') {
      return { kind: 'torrent', ext: 'torrent', name: cd.filename, size };
    }

    const segmentLike = SEGMENT_EXT.has(ext) || SEGMENT_MIME.has(mime);
    if (segmentLike && !cd.attachment) return { kind: 'segment', ext: ext || MIME_EXT[mime] || 'ts', size };

    let media = null;
    if (mime.startsWith('video/')) media = 'video';
    else if (mime.startsWith('audio/')) media = 'audio';
    else if (VIDEO.has(ext) || VIDEO.has(cdExt)) media = 'video';
    else if (AUDIO.has(ext) || AUDIO.has(cdExt)) media = 'audio';

    if (media) {
      const big = size == null || size >= MIN_MEDIA_BYTES;
      if (r.resourceType === 'media' || big || cd.attachment) {
        return { kind: 'file', media, ext: cdExt || ext || MIME_EXT[mime] || (media === 'audio' ? 'm4a' : 'mp4'), name: cd.filename, size };
      }
      return null;
    }

    const fileExt = cdExt || ext || MIME_EXT[mime] || '';
    if (SKIP_EXT.has(fileExt)) return null;
    if (cd.attachment || ARCHIVE.has(fileExt)) {
      if (size != null && size < 64 * 1024) return null;
      return { kind: 'file', media: 'other', ext: fileExt, name: cd.filename, size };
    }
    return null;
  }

  return { classify, urlExt, mimeBase, parseDisposition, totalBytes, VIDEO, AUDIO, ARCHIVE };
})();

if (typeof module !== 'undefined') module.exports = VidHawkClassify;
