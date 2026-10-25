# Contributing to VidHawk

Thanks for helping build a better download manager. The project is a monorepo:

| Folder | What | Toolchain |
|---|---|---|
| `engine/` | Go engine: CLI, browser native host, local API, downloaders | Go 1.24+ (`go test ./...`) |
| `extension/` | Chrome/Edge/Brave MV3 extension | plain JS, `node extension/tests/run.cjs` |
| `desktop/` | Electron shell + Preact renderer | Node 20+, pnpm |
| `docs/` | Architecture and the IDM teardown | Markdown |

## Ground rules

* Keep the engine `CGO_ENABLED=0`: pure-Go dependencies only, so it cross-compiles.
* The renderer talks to the engine over the local WebSocket/REST API only — no Electron IPC business logic.
* Site-specific logic goes behind the `resolver` interface, never into the capture layers.
* Add a table-driven test next to every parser change (`internal/hls`, `internal/dash`, `internal/naming`).
* Run `gofmt`, `go vet ./...`, `go test ./...` and `node extension/tests/run.cjs` before opening a PR.
* No DRM circumvention: encrypted streams other than HLS AES-128 must fail with a clear error.

## Reporting a site that is not detected

Open an issue with: the page URL, the player (hls.js / dash.js / Shaka / native), what the popup showed, and the
`~/.vidhawk/host.log` lines from the attempt. A HAR export of the page helps enormously.
