# Screenshot Tool

`screenshot.js` captures pages of a running BirdNET-Go instance with Playwright
(Chromium), for visual checks and documentation.

## Usage

Run from this directory against any reachable instance (a local dev server is
usually `http://localhost:8080`):

```bash
cd frontend/tools
node screenshot.js http://localhost:8080/ui/dashboard
node screenshot.js http://localhost:8080/ui/analytics -o analytics.png
node screenshot.js http://localhost:8080/ui/settings -w 1920 -h 1080 --no-fullpage
node screenshot.js --help
```

## Options

- `<url>` or `-u, --url <url>`: page to capture (required)
- `-o, --output <file>`: output file name (auto-generated if omitted)
- `-w, --width <px>` / `-h, --height <px>`: viewport (default 1400x1800)
- `-t, --timeout <ms>`: page load timeout (default 60000)
- `--wait <ms>`: extra wait after load
- `--dir <path>`: output directory, resolved relative to this script
  (default `../doc`, that is `frontend/doc/`)
- `--no-fullpage`: capture only the viewport

The script exits non-zero on failure, so check its exit status and success
message rather than assuming the file was written.

## Standard Viewports

The desktop UI targets tablet and desktop only (see
`frontend/src/lib/desktop/AGENTS.md`):

- Desktop: 1400x1800 (default)
- Large desktop: 1920x1080
- Tablet: 768x1024
