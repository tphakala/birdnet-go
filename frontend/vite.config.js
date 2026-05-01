import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { svelteTesting } from '@testing-library/svelte/vite'
import { copyFileSync, mkdirSync, readdirSync, readFileSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { createHash } from 'node:crypto'

// Single source of truth for the translation files directory.
const MESSAGES_SOURCE_DIR = './static/messages'

// Placeholder token replaced by the inject-supported-locales plugin.
const LOCALES_PLACEHOLDER = '__SUPPORTED_LOCALES_PLACEHOLDER__'

/**
 * Return sorted *.json filenames from static/messages/, or [] if missing.
 * Shared by the cache-version, locale-discovery, and copy-messages helpers.
 */
function getMessageFiles() {
  if (!existsSync(MESSAGES_SOURCE_DIR)) return []
  return readdirSync(MESSAGES_SOURCE_DIR)
    .filter(f => f.endsWith('.json'))
    .sort()
}

/**
 * Compute a stable i18n cache version from the content of the message files.
 * Two builds from identical sources produce identical hashes (reproducible
 * builds) and the i18n localStorage cache only evicts when translations
 * actually change. Returns 'dev' when no message files exist.
 */
function computeI18nCacheVersion() {
  const files = getMessageFiles()
  if (files.length === 0) return 'dev'
  const hash = createHash('sha256')
  // NUL-byte delimiter prevents collisions between (filename, content) pairs.
  const delimiter = Buffer.from([0])
  for (const file of files) {
    hash.update(file)
    hash.update(delimiter)
    try {
      hash.update(readFileSync(join(MESSAGES_SOURCE_DIR, file)))
    } catch (/** @type {any} */ err) {
      console.error(`[i18n-cache-version] Failed to read ${file}:`, err.message)
      throw err
    }
    hash.update(delimiter)
  }
  return hash.digest('hex').slice(0, 8)
}

/**
 * Derive supported UI locale codes from the message files.
 * Used by the inject-supported-locales plugin so index.html stays in sync
 * with the actual message files automatically.
 */
function discoverSupportedLocales() {
  const locales = getMessageFiles().map(f => f.replace('.json', ''))
  return locales.length > 0 ? locales : ['en']
}

// https://vite.dev/config/
export default defineConfig({
  base: '/ui/assets/',
  publicDir: 'static',
  define: {
    __I18N_CACHE_VERSION__: JSON.stringify(computeI18nCacheVersion()),
  },
  plugins: [
    tailwindcss(),
    svelte({
      compilerOptions: {
        // HMR is integrated in Svelte 5 core, disabled in production builds
        hmr: process.env.NODE_ENV === 'development' && !process.env.VITEST,
      },
    }),
    svelteTesting(),
    // Derive SUPPORTED_LOCALES in index.html from the actual message files.
    // Adding a new locale only requires the .json file + config.ts entry;
    // index.html stays in sync automatically via this plugin.
    {
      name: 'inject-supported-locales',
      transformIndexHtml(html) {
        const locales = discoverSupportedLocales()
        const formatted = locales.map(l => `'${l}'`).join(', ')
        return html.replace(LOCALES_PLACEHOLDER, formatted)
      },
    },
    // Copy message files to dist during build
    {
      name: 'copy-messages',
      closeBundle() {
        try {
          const messagesDir = './dist/messages';
          mkdirSync(messagesDir, { recursive: true });

          const files = getMessageFiles();
          if (files.length === 0) {
            console.warn('[copy-messages] No message files found in', MESSAGES_SOURCE_DIR);
            return;
          }

          let copiedCount = 0;
          for (const file of files) {
            try {
              copyFileSync(join(MESSAGES_SOURCE_DIR, file), join(messagesDir, file));
              copiedCount++;
            } catch (/** @type {any} */ err) {
              console.error(`[copy-messages] Failed to copy ${file}:`, err.message);
            }
          }

          console.log(`[copy-messages] Copied ${copiedCount} message files to dist/messages`);
        } catch (/** @type {any} */ err) {
          console.error('[copy-messages] Error during message file copying:', err.message);
        }
      }
    }
  ],
  resolve: {
    alias: {
      $lib: '/src/lib',
    },
  },
  server: {
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    sourcemap: 'hidden', // Generate .map files for Sentry, no sourceMappingURL in bundles
    outDir: 'dist',
    chunkSizeWarningLimit: 1000,
    emptyOutDir: true,
    manifest: true, // Generates .vite/manifest.json for cache busting
    rollupOptions: {
      output: {
        // Content hashes enable proper cache busting with CDNs like Cloudflare
        entryFileNames: '[name]-[hash].js',
        chunkFileNames: '[name]-[hash].js',
        assetFileNames: '[name]-[hash].[ext]',
        manualChunks(id) {
          if (id.includes('node_modules/svelte/')) return 'vendor';
          if (
            id.includes('node_modules/chart.js/') ||
            id.includes('node_modules/chartjs-adapter-date-fns/')
          )
            return 'charts';
          // Catch the whole d3 family: bare `d3/` and the `d3-*` packages
          // (d3-scale-chromatic, d3-time-format, d3-array, etc.). Requiring
          // a `/` or `-` after `d3` avoids matching unrelated packages like
          // `d3x` or stray `d3.js` filenames inside other packages.
          if (
            id.includes('node_modules/d3/') ||
            id.includes('node_modules/d3-')
          )
            return 'd3';
          if (id.includes('node_modules/@sentry/')) return 'sentry';
          // Do NOT manually chunk maplibre-gl. It ships as UMD/CJS and
          // Rolldown's manualChunks path emits a broken
          //   export { maplibre_gl_exports as n, t }
          // that references an undeclared `maplibre_gl_exports` identifier,
          // throwing SyntaxError at module load. Letting Rolldown auto-chunk
          // it (returning undefined) produces a working isolated chunk.
          // See https://github.com/rolldown/rolldown/ — known UMD wrapping
          // bug under manualChunks.
          return undefined;
        },
      },
    },
  },
  // Vitest 4.x cache directory (moved from test.cache.dir)
  cacheDir: 'node_modules/.vite',
  test: {
    environment: 'jsdom',
    globals: true,
    watch: false,
    setupFiles: ['./src/test/setup.ts'],
    // Exclude .svelte files from test discovery - they are test wrapper components, not test files
    // Added per CodeRabbit review to fix "No test suite found" errors for .test.svelte files
    include: ['src/**/*.{test,spec}.{js,ts}'],
    // Explicitly exclude node_modules and other non-test directories from file search
    // Integration tests are excluded - run them separately with npm run test:integration
    exclude: ['node_modules', 'dist', 'build', '.svelte-kit', 'coverage', '**/*.integration.{test,spec}.{js,ts}', '**/*.reverse-proxy.{test,spec}.{js,ts}', '**/*.browser.{test,spec}.{js,ts}'],
    // Performance optimizations - Vitest 4.x removed poolOptions, use top-level options
    pool: 'threads', // Faster than default 'forks' for many small tests
    maxWorkers: 8, // Limit max threads to avoid overhead
    // Increase concurrent test limit
    maxConcurrency: 20,
    // Optimize dependency handling
    deps: {
      optimizer: {
        web: {
          // Pre-bundle heavy dependencies
          include: ['@testing-library/svelte', '@testing-library/jest-dom', 'jsdom'],
        },
      },
    },
    coverage: {
      reporter: ['text', 'html', 'lcov'],
      exclude: [
        'node_modules/',
        'src/test/',
        'dist/'
      ]
    }
  }
})
