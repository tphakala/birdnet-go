import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { copyFileSync, mkdirSync, readdirSync, existsSync } from 'fs'
import { join } from 'path'

// https://vite.dev/config/
export default defineConfig({
  base: '/ui/assets/',
  publicDir: 'static',
  plugins: [
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
              } catch (err) {
                console.error(`[copy-messages] Failed to copy ${file}:`, err.message);
              }
            }
          });
          
          console.log(`[copy-messages] Copied ${copiedCount} message files to dist/messages`);
        } catch (err) {
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
      '/ws': {
        target: 'ws://localhost:8080',
        ws: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    chunkSizeWarningLimit: 1000,
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: '[name].js',
        chunkFileNames: '[name].js',
        assetFileNames: '[name].[ext]',
        manualChunks: {
          vendor: ['svelte'],
          charts: ['chart.js'],
          ui: [/* UI library chunks */]
        }
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    watch: false,
    setupFiles: ['./src/test/setup.ts'],
    // Exclude .svelte files from test discovery - they are test wrapper components, not test files
    // Added per CodeRabbit review to fix "No test suite found" errors for .test.svelte files
    include: ['src/**/*.{test,spec}.{js,ts}'],
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
