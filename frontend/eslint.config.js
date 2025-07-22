import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import svelteParser from 'svelte-eslint-parser';
import tsParser from '@typescript-eslint/parser';
import tsPlugin from '@typescript-eslint/eslint-plugin';
import vitest from '@vitest/eslint-plugin';

// Shared browser globals to avoid duplication
const browserGlobals = {
  window: 'readonly',
  document: 'readonly',
  console: 'readonly',
  fetch: 'readonly',
  URLSearchParams: 'readonly',
  CustomEvent: 'readonly',
  alert: 'readonly',
  Headers: 'readonly',
  AbortController: 'readonly',
  AbortSignal: 'readonly',
  FormData: 'readonly',
  navigator: 'readonly',
  Event: 'readonly',
  KeyboardEvent: 'readonly',
  MouseEvent: 'readonly',
  DragEvent: 'readonly',
  HTMLElement: 'readonly',
  HTMLInputElement: 'readonly',
  HTMLSelectElement: 'readonly',
  HTMLTextAreaElement: 'readonly',
  HTMLDivElement: 'readonly',
  HTMLButtonElement: 'readonly',
  HTMLCanvasElement: 'readonly',
  HTMLAudioElement: 'readonly',
  SVGSVGElement: 'readonly',
  SVGElement: 'readonly',
  MutationObserver: 'readonly',
  Node: 'readonly',
  setTimeout: 'readonly',
  setInterval: 'readonly',
  clearTimeout: 'readonly',
  clearInterval: 'readonly',
  localStorage: 'readonly',
  sessionStorage: 'readonly',
  URL: 'readonly',
  Blob: 'readonly',
  getComputedStyle: 'readonly',
};

export default [
  // Base JavaScript config
  js.configs.recommended,
  
  // Svelte files
  {
    files: ['**/*.svelte'],
    languageOptions: {
      parser: svelteParser,
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: 'module',
        parser: tsParser,
        extraFileExtensions: ['.svelte'],
      },
      globals: browserGlobals,
    },
    plugins: {
      svelte,
    },
    rules: {
      ...svelte.configs.recommended.rules,
      // Svelte specific rules
      'svelte/no-unused-svelte-ignore': 'error',
      'svelte/no-dupe-else-if-blocks': 'error',
      'svelte/no-dynamic-slot-name': 'error',
      'svelte/no-object-in-text-mustaches': 'error',
      'svelte/no-useless-mustaches': 'error',
      'svelte/prefer-class-directive': 'error',
      'svelte/prefer-style-directive': 'error',
      // Allow console for now
      'no-console': 'warn',
      'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
    },
  },
  
  // TypeScript files
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: 'module',
        project: './tsconfig.json',
      },
      globals: browserGlobals,
    },
    plugins: {
      '@typescript-eslint': tsPlugin,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...tsPlugin.configs.strict.rules,
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-unused-vars': 'off', // Use TypeScript version instead
      'no-console': 'warn',
      'prefer-const': 'error',
      'no-var': 'error',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/prefer-nullish-coalescing': 'error',
      '@typescript-eslint/prefer-optional-chain': 'error',
      '@typescript-eslint/no-unnecessary-condition': 'error',
      '@typescript-eslint/prefer-readonly': 'error',
      '@typescript-eslint/switch-exhaustiveness-check': 'error',
    },
  },
  
  // JavaScript files
  {
    files: ['**/*.js', '**/*.mjs'],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: 'module',
      globals: browserGlobals,
    },
    rules: {
      // General rules
      'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'warn',
      'prefer-const': 'error',
      'no-var': 'error',
    },
  },
  
  // Test files with Vitest plugin
  {
    files: ['**/*.test.js', '**/*.test.ts', '**/*.spec.js', '**/*.spec.ts'],
    plugins: {
      vitest,
    },
    rules: {
      ...vitest.configs.recommended.rules,
      'vitest/consistent-test-it': ['error', { fn: 'it' }],
      'vitest/no-identical-title': 'error',
      'vitest/no-focused-tests': 'error',
      'vitest/no-disabled-tests': 'warn',
    },
    languageOptions: {
      globals: {
        ...vitest.environments.env.globals,
        // Additional test globals if needed
        global: 'readonly',
      },
    },
  },
  
  // Node.js scripts and config files
  {
    files: ['src/test/**/*.js', '*.config.js', 'test-*.js', 'debug-*.js'],
    languageOptions: {
      globals: {
        // Node.js globals
        global: 'readonly',
        process: 'readonly',
        setTimeout: 'readonly',
        setInterval: 'readonly',
        clearTimeout: 'readonly',
        clearInterval: 'readonly',
        localStorage: 'readonly',
        sessionStorage: 'readonly',
        URL: 'readonly',
        Blob: 'readonly',
        getComputedStyle: 'readonly',
        performance: 'readonly',
        Buffer: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
        exports: 'readonly',
        module: 'readonly',
        require: 'readonly',
      },
    },
  },
  
  // Global ignores
  {
    ignores: [
      'dist/',
      'node_modules/',
      '.svelte-kit/',
      'build/',
      'package/',
      'eslint.config.js',
      'vitest.config.js',
      'vite.config.js',
    ],
  },
];