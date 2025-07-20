# Frontend Tools

This directory contains debugging and testing tools for the BirdNET-Go frontend.

## Screenshot Tool

Automated screenshot capture using Playwright for testing and documentation.

### Prerequisites

```bash
npm install playwright
npx playwright install chromium
```

### Basic Usage

```bash
# Take screenshot of dashboard
node screenshot.js http://192.168.4.152:8080/ui/dashboard

# Take screenshot with custom filename
node screenshot.js http://192.168.4.152:8080/ui/analytics --output analytics-page.png

# Take screenshot with custom viewport
node screenshot.js http://192.168.4.152:8080/ui/settings --width 1920 --height 1080
```

### Command Line Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--output` | `-o` | Output filename | Auto-generated |
| `--width` | `-w` | Viewport width (pixels) | 1400 |
| `--height` | `-h` | Viewport height (pixels) | 1800 |
| `--timeout` | `-t` | Page load timeout (ms) | 60000 |
| `--wait` | | Wait time after load (ms) | 5000 |
| `--dir` | | Output directory | ../doc |
| `--no-fullpage` | | Capture viewport only | false |
| `--help` | | Show help message | |

### Examples

#### Standard Viewports

```bash
# Desktop (default)
node screenshot.js http://192.168.4.152:8080/ui/dashboard

# Large desktop
node screenshot.js http://192.168.4.152:8080/ui/dashboard -w 1920 -h 1080 -o desktop-large.png

# Tablet
node screenshot.js http://192.168.4.152:8080/ui/dashboard -w 768 -h 1024 -o tablet.png

# Mobile
node screenshot.js http://192.168.4.152:8080/ui/dashboard -w 390 -h 844 -o mobile.png
```

#### Testing Different Pages

```bash
# Dashboard
node screenshot.js http://192.168.4.152:8080/ui/dashboard -o dashboard.png

# Analytics
node screenshot.js http://192.168.4.152:8080/ui/analytics -o analytics.png

# Settings
node screenshot.js http://192.168.4.152:8080/ui/settings -o settings.png

# Search results
node screenshot.js "http://192.168.4.152:8080/ui/search?query=bird" -o search-results.png
```

### Output

Screenshots are saved to `frontend/doc/` by default. The tool will:

- Auto-generate filenames based on URL if not specified
- Create the output directory if it doesn't exist
- Capture full page content by default
- Wait for dynamic content to load
- Provide debugging information about page dimensions

### Troubleshooting

**Timeout errors:**
- Increase timeout with `--timeout 120000`
- Check if the development server is running
- Verify the URL is accessible

**Missing content:**
- Increase wait time with `--wait 10000`
- Use `--no-fullpage` for viewport-only capture
- Check browser console for JavaScript errors

**Installation issues:**
- Run `npx playwright install-deps` for system dependencies
- Ensure Node.js version compatibility (v16+)

### Integration with Testing

The screenshot tool can be integrated into automated testing workflows:

```bash
#!/bin/bash
# Test all main pages
node screenshot.js http://192.168.4.152:8080/ui/dashboard -o test-dashboard.png
node screenshot.js http://192.168.4.152:8080/ui/analytics -o test-analytics.png
node screenshot.js http://192.168.4.152:8080/ui/settings -o test-settings.png
```

For more tools and debugging scripts, see the existing files in this directory.