import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import tailwindcss from '@tailwindcss/vite'
import { svelteTesting } from '@testing-library/svelte/vite'
import { copyFileSync, mkdirSync, readdirSync, existsSync } from 'fs'
import { join } from 'path'

// https://vite.dev/config/
export default defineConfig({
  base: '/ui/assets/',
  publicDir: 'static',
  define: {
    __I18N_CACHE_VERSION__: JSON.stringify(Date.now().toString(36)),
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
    // Copy message files to dist during build
    {
      name: 'copy-messages',
      closeBundle() {
        try {
          // Create messages directory in dist
          const messagesDir = './dist/messages';
          mkdirSync(messagesDir, { recursive: true });
          
          // Copy all message files
          const sourceDir = './static/messages';
          
          // Check if source directory exists
          if (!existsSync(sourceDir)) {
            console.warn('[copy-messages] Source directory not found:', sourceDir);
            return;
          }
          
          const files = readdirSync(sourceDir);
          let copiedCount = 0;
          
          files.forEach(file => {
            if (file.endsWith('.json')) {
              try {
                copyFileSync(join(sourceDir, file), join(messagesDir, file));
                copiedCount++;
              } catch (/** @type {any} */ err) {
                console.error(`[copy-messages] Failed to copy ${file}:`, err.message);
              }
            }
          });
          
          console.log(`[copy-messages] Copied ${copiedCount} message files to dist/messages`);
        } catch (/** @type {any} */ err) {
          console.error('[copy-messages] Error during message file copying:', err.message);
          // Don't fail the build, just log the error
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
    // Watch mode uses Rolldown's default watcher options under Vite 8.
    // The previous chokidar polling config was removed because Rolldown's
    // WatcherOptions type no longer accepts a nested `chokidar` key.
    watch: process.argv.includes('--watch') ? {} : null,
    rollupOptions: {
      output: {
        // Content hashes enable proper cache busting with CDNs like Cloudflare
        entryFileNames: '[name]-[hash].js',
        chunkFileNames: '[name]-[hash].js',
        assetFileNames: '[name]-[hash].[ext]',
        manualChunks(id) {
          if (id.includes('node_modules/svelte/')) return 'vendor';
          if (id.includes('node_modules/chart.js/')) return 'charts';
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
