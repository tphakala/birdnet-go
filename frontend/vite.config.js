import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'
import { copyFileSync, mkdirSync, readdirSync } from 'fs'
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
        // Create messages directory in dist
        const messagesDir = './dist/messages';
        mkdirSync(messagesDir, { recursive: true });
        
        // Copy all message files
        const sourceDir = './static/messages';
        const files = readdirSync(sourceDir);
        files.forEach(file => {
          if (file.endsWith('.json')) {
            copyFileSync(join(sourceDir, file), join(messagesDir, file));
          }
        });
        console.log('[copy-messages] Copied message files to dist/messages');
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
    setupFiles: ['./src/test/setup.js'],
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
