# CLAUDE.md

Athvan is a Go HTTP API that renders iPhone/iPad lock-screen wallpapers with events,
reminders and notes drawn on top. README.md has the user-facing docs.

## Commands

```sh
go run ./cmd/athvan -addr 127.0.0.1:8787    # serve; preview page at /
go test ./...                               # ~15 s: renders every device × orientation
go test ./internal/render -bench Render -run ^$
go vet ./... && gofmt -l .
go generate ./...                           # regenerate openapi.yaml; run after API/schema changes
```

## Architecture

- `internal/render/spec.go`: the request model (`WallpaperRequest`, `Background`, `Style`,
  `Layout`, `Event`, `Reminder`). Its struct tags (`doc`, `enum`, `default`, `minimum`,
  `pattern`, …) **are** the OpenAPI schema and the request validation. There is no
  hand-written spec.
- `internal/render/render.go`:
  - `normalize()` applies defaults, parses colours/times and returns `InputError`.
  - `draw()` lays out title + sections, packs columns, trims overflow, paints glass/shadow.
  - Sections are lists of `item`s made of `row`s. Each row knows its height and how to draw
    itself, so measuring and drawing share one code path.
- `internal/render/background.go`: colour/gradient/image/Bing base layer, `cover` crop,
  `fastBlur`.
- `internal/render/bing.go`: Bing metadata and photo, cached per market/index for 30 min.
- `internal/render/fetch.go`: the HTTP client for remote images. A dialer `Control` hook blocks
  private/loopback IPs at connect time (SSRF guard; `ATHVAN_ALLOW_PRIVATE_URLS=1` opts out).
- `internal/api/api.go`: Huma operations. Maps `render.InputError` → 422 and
  `render.UpstreamError` → 502. Adds the device-id enum to the schema after registration
  because the list lives in Go.
- `internal/devices`: presets. Pixel sizes are native portrait; `Scale` is px per point;
  `SafeTop`/`SafeBottom` are fractions of the height kept clear for the iOS clock and buttons.
- `internal/web/index.html`: the preview page, a single embedded file of vanilla JS with no
  build step.

## Conventions and gotchas

- **Defaults live in three places and must agree:** `default:` tags in `spec.go` (docs),
  the `def(...)` calls in `normalize()` (behaviour), and `DEFAULTS` / `SERVER_DEFAULTS` in
  `index.html` (the preview page omits values equal to server defaults).
- Zero means "unset" in `normalize()`, so a field whose meaningful value is 0 (e.g. a 0°
  gradient) can't be expressed. Pick a different encoding if that matters.
- Sizes in the request are in **points**; multiply by `plan.scale` (or `metrics.pt`) to get
  pixels. Font sizes in `metrics` are already in pixels.
- **Don't use `gg.Context.DrawImage` for full-canvas compositing.** It resamples through a
  generic kernel and made renders ~7× slower. Use `image/draw` (`draw.Draw` / `draw.DrawMask`);
  gg is fine for paths and text.
- Blur via `fastBlur` (downscale → blur → upscale). Keep the downscale factor ≤ 4: beyond that
  the upscale shows blotches in the glass.
- Bing photos are copyrighted. Never commit renders that use them (README examples use
  gradients/colours only).
- Windows dev box: when patching files from Python, open with `encoding='utf-8',
  newline='\n'`, otherwise non-ASCII characters (`…`, `–`, `×`) and line endings get mangled.
  Run `gofmt -w` afterwards.
- After changing anything in `spec.go` or `api.go`, run `go generate ./...` and commit the
  updated `openapi.yaml`.
