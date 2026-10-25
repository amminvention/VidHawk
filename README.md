<p align="center">
  <img src="extension/icons/128.png" width="96" alt="VidHawk" />
</p>

<h1 align="center">VidHawk</h1>

<p align="center"><strong>Hunts down any video, from any player.</strong><br/>
An open-source download manager that catches HLS, DASH and direct media in your browser, downloads with many parallel connections, speaks BitTorrent, and names files properly.</p>

<p align="center">
  <a href="https://github.com/amminvention/VidHawk/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/amminvention/VidHawk/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="MIT" src="https://img.shields.io/badge/license-MIT-f59e0b.svg"></a>
  <img alt="Go" src="https://img.shields.io/badge/engine-Go-00ADD8.svg">
  <img alt="Chrome MV3" src="https://img.shields.io/badge/extension-Chrome%20MV3-4285F4.svg">
</p>

---

VidHawk was built after taking Internet Download Manager apart to see why it misses streams and names files
`Something - Detail Page.mp4`. The result is a modern replacement with none of IDM's kernel drivers, injected DLLs or
registry databases — and with the things IDM never had: torrents, a scriptable local API, cross-platform binaries and
an MIT license. The teardown is in [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## What it does

| | |
|---|---|
| **Catches streams any player uses** | Three capture layers in the extension: response headers, the *bodies* of manifests as the page fetches them (`#EXTM3U` / `<MPD>` regardless of MIME type or `206` responses), and stream URLs hidden inside JSON API responses. Works with hls.js, dash.js, Shaka, video.js, JW Player, native `<video>` … |
| **Quality picker** | Every HLS/DASH stream is probed; pick 2160p/1080p/720p… or the smallest. Separate audio renditions are muxed in. |
| **Names files from real metadata** | JSON-LD, Open Graph, headings and the title are scored (season/episode markers and distinctive words win, site slogans lose): `Dar-E-Nijaat [1080p].mp4`, not `Dar-e-nijaat - Detail Page.mp4`. |
| **Series aware** | Detects `S01E02`, `1x02`, `Season 1 Episode 2` and lets you edit Show / Season / Episode; names `Show - S01E02.mp4` and can file under `Show\Season 01\`. **Related episodes** found on the page can be caught and downloaded in one batch (each page opened in the background, stream caught, tab closed). |
| **Smart name (on-device AI)** | Optional: Chrome's built-in Gemini Nano (Prompt API, runs locally) picks the main video among the candidates and proposes title / show / season / episode — never applied without your click. |
| **Save anywhere** | Per-download folder with recent folders remembered, plus the engine's default folder editable right in the popup. Remove a download from the list or delete the file too. |
| **Fast direct downloads** | Up to 32 parallel ranged connections per file, resumable state, ETag revalidation. |
| **Torrents & magnets** | Built-in BitTorrent client (DHT, PEX, UPnP) — paste a magnet link, drop a `.torrent`, or click one in the browser. |
| **Hard sites** | A yt-dlp resolver handles JavaScript-heavy players and signed URLs when the sniffers can't. |
| **Lossless remux** | ffmpeg stream-copies HLS/DASH into MP4 with `+faststart`; no re-encoding. |
| **Desktop app + CLI + API** | Electron app with tray, an engine CLI, and a token-protected local REST/WebSocket API every client shares — one engine, one job list. |
| **Honest about DRM** | Widevine / PlayReady / FairPlay / SAMPLE-AES streams are reported as protected and refused. |

## How it is built

```
Browser extension (MV3)  ──native messaging──▶  vidhawk (one static Go binary)  ◀──WebSocket/REST──  Desktop app (Electron)
  webRequest classifier                            httpdl · hls · dash · torrent · yt-dlp                 Downloads · Add · Settings
  fetch/XHR body sniffing                          job manager · SQLite · ffmpeg remux                    tray · magnet/.torrent handler
  DOM + metadata                                   CLI · native host · local API
```

No kernel driver, no process injection, no hooks — only public browser APIs. Details and the IDM comparison: [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Install

### Option A — desktop app (Windows)

Download `VidHawk-Setup-x.y.z.exe` from [Releases](https://github.com/amminvention/VidHawk/releases). The installer bundles the engine, registers browser integration and `magnet:` links. Then load the browser extension (below).

### Option B — engine only (Windows / macOS / Linux)

```bash
# from a release asset
vidhawk doctor           # checks ffmpeg, yt-dlp, browser registration
vidhawk install          # registers the native-messaging host for Chrome/Edge/Brave/Chromium/Vivaldi

# or build it yourself (Go 1.24+)
cd engine && CGO_ENABLED=0 go build -o dist/vidhawk ./cmd/vidhawk
```

Optional tools: [ffmpeg](https://ffmpeg.org) (remux to MP4; without it HLS/DASH are saved as raw `.ts`/fMP4) and
[yt-dlp](https://github.com/yt-dlp/yt-dlp) (resolver for JavaScript-heavy sites). `vidhawk doctor` tells you what it found.

### Install the browser extension

1. Open `chrome://extensions` (or `edge://extensions`), enable **Developer mode**.
2. **Load unpacked** → select the `extension/` folder of this repository.
3. The extension ID is fixed (`nbpppdcnpheeihphpfihofafmhmnhhfd`), so the host registered by `vidhawk install` already allows it. Click the VidHawk icon — the status pill should read **Engine vX**.

A Chrome Web Store listing is on the roadmap.

## Use

* **In the browser** – open a page, play the video, click the VidHawk icon: every stream and file the page used is listed with a quality picker and a sensible name. Right-click any link/video for *Download with VidHawk*. Enable *Take over downloads* to route the browser's own media/archive downloads through the engine.
* **Desktop app** – *Add* accepts many links at once (http, magnet, `.torrent` paths), inspects a link for qualities, lets you set a Referer for protected streams and choose the folder. *Downloads* shows live progress, speed, ETA, peers; pause/resume/cancel; open in folder.
* **CLI**

```bash
vidhawk download "https://example.com/video/master.m3u8" -q 1080p -n "Episode 1" --referer https://example.com/
vidhawk download "magnet:?xt=urn:btih:…" -o ~/Downloads/VidHawk
vidhawk download "https://host/file.iso" -c 16          # 16 connections
vidhawk probe "https://example.com/video/master.m3u8"   # list variants / size / DRM
vidhawk serve                                           # local API for the desktop app
vidhawk config connections=16 downloadDir="D:\Video"
```

## Configuration

`~/.vidhawk/config.json` (Windows: `%USERPROFILE%\.vidhawk\config.json`):

```json
{
  "downloadDir": "C:\\Users\\you\\Downloads\\VidHawk",
  "connections": 8,
  "segmentConcurrency": 8,
  "maxJobs": 3,
  "ffmpegPath": "",
  "ytDlpPath": "",
  "verifyTLS": true,
  "torrent": { "listenPort": 6881, "seedAfterComplete": false }
}
```

## Local API

`vidhawk serve` (or the desktop app) listens on `127.0.0.1:<random port>` with a bearer token; both are written to
`~/.vidhawk/api.json`. Endpoints: `GET /jobs`, `POST /jobs`, `POST /jobs/{id}/{pause|resume|cancel}`, `DELETE /jobs/{id}`,
`POST /probe`, `GET|PUT /config`, `POST /open`, `GET|POST /integration`, `GET /ws` (live job events). Any tool can queue
downloads:

```bash
curl -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d '{"url":"https://example.com/file.zip"}' http://127.0.0.1:$PORT/jobs
```

## Repository

```
engine/      Go engine — cmd/vidhawk, internal/{engine,httpdl,hls,dash,torrent,resolver,api,host,installer,…}
extension/   MV3 extension — background.js, inject.js, content.js, popup.*, lib/, tests/
desktop/     Electron + Preact app — src/main (sidecar, tray), src/renderer (pages), electron-builder.yml
docs/        ARCHITECTURE.md (design + IDM teardown)
design-system/  the ui-ux-pro-max design tokens the UI follows
tools/       icon generator, CDP end-to-end tests
```

Development:

```bash
cd engine && go test ./... && go build -o dist/vidhawk.exe ./cmd/vidhawk
node extension/tests/run.cjs
cd desktop && pnpm install && pnpm typecheck && pnpm build && pnpm start   # or pnpm dist for the installer
```

## Roadmap

Scheduler and bandwidth limits · Firefox build · Chrome Web Store listing · site resolver plugins · macOS/Linux desktop
packages · auto-update · browser-less capture through an optional local proxy (never a kernel driver).

## Legal

VidHawk downloads what your browser already received. Respect the terms of the sites you use and the rights of content
owners; the project does not circumvent DRM and will not accept contributions that do. MIT licensed — see [LICENSE](LICENSE).
