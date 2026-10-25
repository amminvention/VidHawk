# VidHawk architecture

VidHawk is three things wired to one job model:

```
┌──────────────────────────── Browser (Chrome / Edge / Brave) ──────────────────────────────┐
│  MAIN world (inject.js)         Isolated world (content.js)        Service worker          │
│  fetch/XHR body sniffing:       <video>/<audio>/<source> src,      (background.js)         │
│  #EXTM3U / <MPD> → manifest,    JSON-LD / OG / h1 / title meta,    webRequest classifier,  │
│  media URLs in JSON APIs        SPA navigation tracking            header capture, per-tab │
│         └── window.postMessage ───────────┴───── runtime.sendMessage ──► media registry,   │
│  Popup: detected media, quality picker, rename, download, jobs ◄───────── native port      │
└───────────────────────────────────────────┬───────────────────────────────────────────────┘
                                            │ native messaging (stdio, 4-byte length + JSON)
┌───────────────────────────────────────────▼───────────────────────────────────────────────┐
│ vidhawk (single static Go binary)                                                          │
│  cmd/vidhawk ── CLI · `host` (native messaging) · `serve` (REST + WebSocket on 127.0.0.1)  │
│  internal/engine ── Manager: detect kind → run → progress events → SQLite (internal/store) │
│  internal/httpdl   multi-connection ranged HTTP with resumable .vidhawk state              │
│  internal/hls      master/media playlists, AES-128, fMP4/TS, audio renditions             │
│  internal/dash     MPD SegmentTemplate/Timeline/List/Base, video + audio                   │
│  internal/torrent  anacrolix/torrent: magnets, .torrent, DHT                               │
│  internal/resolver yt-dlp fallback for JavaScript-heavy sites                              │
│  internal/ffmpeg   lossless remux/mux to MP4 (+faststart)                                  │
│  internal/installer native-host manifest + per-browser registration                        │
└───────────────────────────────────────────▲───────────────────────────────────────────────┘
                                            │ WebSocket + REST, bearer token from ~/.vidhawk/api.json
┌───────────────────────────────────────────┴───────────────────────────────────────────────┐
│ Desktop app (Electron shell, Preact renderer) — spawns `vidhawk serve`, tray, add/queue UI │
└───────────────────────────────────────────────────────────────────────────────────────────┘
```

## Principles

1. **No kernel driver, no process injection, no Windows hooks.** Everything a browser does is visible to an
   MV3 extension: response headers via `webRequest`, response bodies via page-world `fetch`/XHR wrappers, and the
   DOM. That is enough to catch any player, and it survives browser and OS updates.
2. **Three independent detection layers** feed one per-tab registry; a stream only needs to be seen by one of them.
   * Layer 1 — network classifier (`extension/lib/classify.js`): status 200 **and 206**, HLS/DASH by MIME *or*
     extension *or* Content-Disposition, torrents, media files with a size floor, archives/installers.
     Request headers (Referer, Origin, Cookie, User-Agent, Authorization) are attached to every item so the engine
     can fetch token-protected streams exactly as the page did.
   * Layer 2 — page hooks (`extension/inject.js`): manifests are recognised by *body* (`#EXTM3U`, `<MPD`) whatever
     the MIME type, master playlists are expanded so one entry per stream is shown, and stream URLs inside JSON API
     responses are reported before the player even requests them.
   * Layer 3 — DOM (`extension/content.js`): media element sources in every frame, metadata snapshots, SPA URL
     changes.
3. **Metadata-first naming.** JSON-LD `VideoObject.name` → `og:title` → `twitter:title` → `h1` → cleaned
   `document.title`, with site names and boilerplate ("Detail Page", "Watch Online", "- YouTube") stripped and the
   quality appended (`Title [1080p].mp4`). The same rules live in `extension/lib/naming.js` and
   `engine/internal/naming`.
4. **One engine, one job list.** `vidhawk host` (spawned by the browser) first looks for a running engine via
   `~/.vidhawk/api.json`; if one answers it proxies to it, otherwise it runs the engine in-process and keeps running
   until active jobs finish even after the browser disconnects. The desktop app and the extension therefore always
   see the same jobs.
5. **Open formats.** SQLite job database, JSON config, ffmpeg for remuxing, pure-Go dependencies (`CGO_ENABLED=0`).
6. **Pluggable resolvers** for site-specific logic instead of obfuscated hard-coding; yt-dlp is the first one.
7. **No DRM circumvention.** Anything beyond HLS AES-128 (SAMPLE-AES, Widevine, PlayReady, FairPlay) fails with a
   clear message.

## Job model and protocol

Every client speaks the same JSON job:

```json
{"id":"1f3a9c2b4d5e","kind":"hls","url":"…","title":"Dar-E-Nijaat","quality":"best","outDir":"…",
 "status":"running","done":412,"total":1440,"unit":"segments","bytes":734003200,"speed":5242880,
 "percent":28.6,"detail":"","peers":0,"path":"","error":"","created":1761400000000,"updated":1761400012000}
```

Commands: `ping`, `probe {url, headers}`, `download {url, kind, title, quality, pageUrl, outDir, headers}`,
`cancel|pause|resume|remove {jobId}`, `jobs`, `open {path}`, `config {set}`. Events: `{"event":"job","job":{…}}`
(throttled to 4/s per job, immediate on status change).

* Native messaging: 4-byte native-endian length prefix + UTF-8 JSON (`internal/protocol`).
* Local API: `GET /health`, `GET /engine`, `GET|POST /jobs`, `POST /jobs/{id}/{cancel|pause|resume}`,
  `DELETE /jobs/{id}`, `POST /probe`, `GET|PUT /config`, `POST /open`, `GET /ws` (hello + events).
  Bound to `127.0.0.1`, random port, bearer token; endpoint written to `~/.vidhawk/api.json`.

## Kind detection

`magnet:` or `.torrent` → torrent · `.m3u8` → hls · `.mpd` → dash · media/archive extension → file · otherwise the
first 64 KB is fetched: `#EXTM3U` → hls, `<MPD` → dash, `text/html` → yt-dlp resolver, anything else → file.

## Why IDM fails where VidHawk doesn't (teardown summary)

Internet Download Manager 6.43 was reverse engineered from its install to understand the state of the art:

| Area | IDM | Consequence |
|---|---|---|
| Browser integration | Kernel WFP driver `idmwfp.sys` (WSK listener on `127.0.0.1:1001`, stream injection of `HTTP/1.0 204 Intercepted by the IDM Advanced Integration`), `IDMNetMon64.dll` injected with `SetWindowsHookEx`, extension talking a custom `MSG#id#type#flags#n:…` protocol over WebSocket | Windows-only, fragile across updates, AV false positives, a native overlay panel positioned by streaming window geometry through the extension |
| Manifest detection | MIME/extension heuristics plus hard-coded, ROT13-obfuscated site regexes pushed from the native app | Misses manifests delivered under odd MIME types or through JSON APIs; aryplus.tv serves HLS playlists as `206 Partial Content` |
| Naming | `window.top.document.title` | `Dar-e-nijaat - Detail Page.mp4` instead of `Dar-E-Nijaat.mp4` |
| Engine | 32-bit MFC app built with VS2008, bundled OpenSSL 1.1.1g (EOL), in-house muxer `idmvconv.dll`, MPD parser on libxml2 | No cross-platform story |
| Storage | Registry (`HKCU\Software\DownloadManager\<n>` per download) | Slow, un-exportable, no job log |
| Torrents | none | — |
| Automation | COM `IDMDwnlMgr` + `/d` command line | No local API |

VidHawk keeps the good ideas (three capture points, a quality picker, remux to MP4, header forwarding) and drops
the invasive parts.

## Repository layout

```
engine/      Go module github.com/amminvention/vidhawk (cmd/vidhawk, internal/*)
extension/   MV3 extension (background.js, inject.js, content.js, popup.*, lib/, icons/, tests/)
desktop/     Electron + Preact app (src/main, src/renderer), electron-builder config
docs/        this document
tools/       icon generator
```
