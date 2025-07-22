import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'
import { svelteTesting } from '@testing-library/svelte/vite'

// https://vite.dev/config/
export default defineConfig({
  base: '/ui/assets/',
  plugins: [
    svelte({
      compilerOptions: {
        // HMR is integrated in Svelte 5 core, disabled in production builds
        hmr: process.env.NODE_ENV === 'development' && !process.env.VITEST,
      },
    }),
    svelteTesting(),
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
