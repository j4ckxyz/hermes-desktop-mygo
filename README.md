<img src="assets/icon.svg" width="64" height="64" alt="">

# hermes-app

Created in [T3 Code](https://t3.codes).

**Hermes Desktop**: a native desktop client (macOS, Windows, Linux) for
[Hermes Agent](https://github.com/NousResearch/hermes-agent), written in Go
with [MyGo](https://github.com/egoist/mygo)'s GPU-drawn native UI (pinned at
v0.10.0). The visual spec is the design export in `design/`.

## Develop

Go ≥ 1.27.3. Linux needs GTK 3, Pango, cairo and libepoxy (and Xvfb for
headless runs).

```sh
make check        # go vet (all build tags) + go test -race ./...
make build        # bin/hermes-desktop
make cross        # dist/: macOS, Windows, Linux × amd64/arm64, from any OS
make demo         # the app against an in-process fake instance (design data)
make screenshots  # each screen of the demo build, on a private Xvfb
make bench        # launch, chat switching and streaming numbers
```

Live end-to-end test against your own instance: put `HERMES_URL` and either
`HERMES_USERNAME`/`HERMES_PASSWORD` or `HERMES_API_KEY` in `.env.local`
(gitignored), then `go test -tags live -run Live -v ./internal/hermes/`.

## Layout

| Path | What |
|---|---|
| `cmd/hermes-desktop` | the app: window, native menus, dialogs, notifications |
| `internal/hermes` | the Hermes client: REST, sign-in (API key, dashboard login), JSON-RPC socket |
| `internal/transcript` | stored messages and live events → turns, tool-call labels |
| `internal/core` | app state, sync, cache-first loading; all I/O off the UI thread |
| `internal/views` | the MyGo views: connect, sidebar, chat, settings, search |
| `internal/cache` | SQLite cache (pure Go) |
| `internal/secrets` | OS keychain |
| `internal/theme`, `internal/settings` | design tokens, persisted Look & Feel |
| `docs/` | API and MyGo research, benchmarks |
