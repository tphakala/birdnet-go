# Screenshot Tool for LLMs

## Quick Usage

Use the Bash tool to run screenshot commands:

```bash
cd /home/thakala/go/src/github.com/tphakala/birdnet-go/frontend/tools
node screenshot.js http://192.168.4.152:8080/ui/dashboard
```

## Common Commands

```bash
# Dashboard screenshot (default 1400x1800)
node screenshot.js http://192.168.4.152:8080/ui/dashboard

# Analytics page
node screenshot.js http://192.168.4.152:8080/ui/analytics -o analytics.png

# Settings page with custom dimensions
node screenshot.js http://192.168.4.152:8080/ui/settings -w 1920 -h 1080

# Mobile viewport
node screenshot.js http://192.168.4.152:8080/ui/dashboard -w 390 -h 844 -o mobile-dashboard.png

# Tablet viewport
node screenshot.js http://192.168.4.152:8080/ui/dashboard -w 768 -h 1024 -o tablet-dashboard.png
```

## Parameters

- `<url>` - Required URL to screenshot
- `-o, --output <file>` - Output filename (auto-generated if not specified)
- `-w, --width <pixels>` - Viewport width (default: 1400)
- `-h, --height <pixels>` - Viewport height (default: 1800)
- `--no-fullpage` - Capture viewport only instead of full page

## Output Location

Screenshots are saved to `../doc/` relative to the script (i.e., `frontend/doc/`)

## Error Handling

- Script will timeout after 60 seconds
- Will create output directory if it doesn't exist
- Returns proper exit codes for automation

## Viewport Standards

Use these standard viewports for consistent testing:

- **Desktop**: 1400x1800 (default)
- **Large Desktop**: 1920x1080
- **Tablet**: 768x1024
- **Mobile**: 390x844

## Tips for LLMs

1. Always `cd` to the tools directory first
2. Use descriptive output filenames for documentation
3. Check if screenshot was successful by looking for success message
4. Screenshots are automatically saved to frontend/doc/ for easy access
