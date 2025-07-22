#!/usr/bin/env node
/* eslint-disable no-undef, no-console */
const { chromium } = require('playwright');
const path = require('path');
const fs = require('fs');

// Default configuration
const DEFAULT_CONFIG = {
  width: 1400,
  height: 1800,
  timeout: 60000,
  waitTime: 5000,
  outputDir: '../doc',
  fullPage: true,
};

// Parse command line arguments
function parseArgs() {
  const args = process.argv.slice(2);
  const config = { ...DEFAULT_CONFIG };
  let url = '';
  let filename = '';

  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    const nextArg = args[i + 1];

    switch (arg) {
      case '--url':
      case '-u':
        url = nextArg;
        i++;
        break;
      case '--output':
      case '-o':
        filename = nextArg;
        i++;
        break;
      case '--width':
      case '-w':
        config.width = parseInt(nextArg);
        i++;
        break;
      case '--height':
      case '-h':
        config.height = parseInt(nextArg);
        i++;
        break;
      case '--timeout':
      case '-t':
        config.timeout = parseInt(nextArg);
        i++;
        break;
      case '--wait':
        config.waitTime = parseInt(nextArg);
        i++;
        break;
      case '--dir':
        config.outputDir = nextArg;
        i++;
        break;
      case '--no-fullpage':
        config.fullPage = false;
        break;
      case '--help':
        showHelp();
        process.exit(0);
        break;
      default:
        if (!url && !arg.startsWith('-')) {
          url = arg;
        }
        break;
    }
  }

  if (!url) {
    console.error('Error: URL is required');
    showHelp();
    process.exit(1);
  }

  // Generate filename if not provided
  if (!filename) {
    const urlObj = new URL(url);
    const pathPart = urlObj.pathname.replace(/\//g, '-').replace(/^-+|-+$/g, '') || 'index';
    filename = `screenshot-${urlObj.hostname}-${pathPart}.png`;
  }

  if (!filename.endsWith('.png')) {
    filename += '.png';
  }

  return { url, filename, config };
}

function showHelp() {
  console.log(`
Usage: node screenshot.js <url> [options]

Arguments:
  url                    URL to screenshot (required)

Options:
  -o, --output <file>    Output filename (default: auto-generated)
  -w, --width <pixels>   Viewport width (default: ${DEFAULT_CONFIG.width})
  -h, --height <pixels>  Viewport height (default: ${DEFAULT_CONFIG.height})
  -t, --timeout <ms>     Page load timeout (default: ${DEFAULT_CONFIG.timeout})
  --wait <ms>            Wait time after load (default: ${DEFAULT_CONFIG.waitTime})
  --dir <path>           Output directory (default: ${DEFAULT_CONFIG.outputDir})
  --no-fullpage          Capture viewport only, not full page
  --help                 Show this help message

Examples:
  node screenshot.js http://localhost:8080/ui/dashboard
  node screenshot.js http://localhost:8080/ui/analytics -o analytics.png
  node screenshot.js http://localhost:8080 -w 1920 -h 1080 --no-fullpage
  node screenshot.js http://localhost:8080/ui/settings --dir ./screenshots
`);
}

async function takeScreenshot() {
  const { url, filename, config } = parseArgs();

  // Resolve output path
  const outputDir = path.resolve(__dirname, config.outputDir);
  const outputPath = path.join(outputDir, filename);

  // Ensure output directory exists
  if (!fs.existsSync(outputDir)) {
    fs.mkdirSync(outputDir, { recursive: true });
  }

  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: config.width, height: config.height },
  });
  const page = await context.newPage();

  try {
    console.log(`Taking screenshot of: ${url}`);
    console.log(`Viewport: ${config.width}x${config.height}`);
    console.log(`Output: ${outputPath}`);

    await page.goto(url, {
      waitUntil: 'domcontentloaded',
      timeout: config.timeout,
    });

    // Wait for dynamic content to load
    await page.waitForTimeout(config.waitTime);

    // Get page dimensions for debugging
    const dimensions = await page.evaluate(() => {
      const body = document.body;
      const html = document.documentElement;

      const height = Math.max(
        body.scrollHeight,
        body.offsetHeight,
        html.clientHeight,
        html.scrollHeight,
        html.offsetHeight
      );

      const mainContent =
        document.querySelector('main') || document.querySelector('[role="main"]') || document.body;
      const contentRect = mainContent.getBoundingClientRect();

      return {
        fullHeight: height,
        contentHeight: Math.max(contentRect.height, height),
        viewportHeight: window.innerHeight,
      };
    });

    console.log('Page dimensions:', dimensions);

    // Scroll to ensure all content is rendered
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await page.waitForTimeout(1000);
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(500);

    await page.screenshot({
      path: outputPath,
      fullPage: config.fullPage,
    });

    console.log(`Screenshot saved successfully: ${outputPath}`);
  } catch (error) {
    console.error('Error taking screenshot:', error);
    process.exit(1);
  } finally {
    await browser.close();
  }
}

// Run if called directly
if (require.main === module) {
  takeScreenshot().catch(error => {
    console.error('Unhandled error:', error);
    process.exit(1);
  });
}

module.exports = { takeScreenshot, parseArgs, DEFAULT_CONFIG };
