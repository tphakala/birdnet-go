import js from '@eslint/js';
import svelte from 'eslint-plugin-svelte';
import svelteParser from 'svelte-eslint-parser';
import tsParser from '@typescript-eslint/parser';
import tsPlugin from '@typescript-eslint/eslint-plugin';
import vitest from '@vitest/eslint-plugin';
import security from 'eslint-plugin-security';
import playwright from 'eslint-plugin-playwright';

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
  FocusEvent: 'readonly',
  HTMLElement: 'readonly',
  HTMLInputElement: 'readonly',
  HTMLSelectElement: 'readonly',
  HTMLTextAreaElement: 'readonly',
  HTMLDivElement: 'readonly',
  HTMLButtonElement: 'readonly',
  HTMLCanvasElement: 'readonly',
  HTMLAudioElement: 'readonly',
  HTMLImageElement: 'readonly',
  HTMLUListElement: 'readonly',
  SVGSVGElement: 'readonly',
  SVGElement: 'readonly',
  MutationObserver: 'readonly',
  Node: 'readonly',
  setTimeout: 'readonly',
  setInterval: 'readonly',
  clearTimeout: 'readonly',
  clearInterval: 'readonly',
  queueMicrotask: 'readonly',
  localStorage: 'readonly',
  sessionStorage: 'readonly',
  URL: 'readonly',
  Blob: 'readonly',
  getComputedStyle: 'readonly',
  TouchEvent: 'readonly',
  crypto: 'readonly',
  // TypeScript DOM interface types
  HTMLMetaElement: 'readonly',
  Document: 'readonly',
  Window: 'readonly',
  AddEventListenerOptions: 'readonly',
  TextDecoder: 'readonly',
  TextEncoder: 'readonly',
};

// Svelte 5 rune globals for .svelte.ts files (tsParser doesn't recognize runes;
// .svelte files get rune support from svelte-eslint-parser automatically)
const svelteRuneGlobals = {
  $state: 'readonly',
  $derived: 'readonly',
  $effect: 'readonly',
  $props: 'readonly',
  $bindable: 'readonly',
  $inspect: 'readonly',
  $host: 'readonly',
};

// Shared TypeScript rules used by .svelte.ts, .ts, and Playwright configs
const sharedTypeScriptRules = {
  '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
  'no-unused-vars': 'off',
  'no-console': 'warn',
  'prefer-const': 'error',
  'no-var': 'error',
  'no-useless-assignment': 'off',
  '@typescript-eslint/no-explicit-any': 'error',
  '@typescript-eslint/prefer-nullish-coalescing': 'error',
  '@typescript-eslint/prefer-optional-chain': 'error',
  '@typescript-eslint/no-unnecessary-condition': 'error',
  '@typescript-eslint/prefer-readonly': 'error',
  '@typescript-eslint/switch-exhaustiveness-check': 'error',
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
      security,
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
      // Security rules
      ...security.configs.recommended.rules,
      // Allow console for now
      'no-console': 'warn',
      'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-useless-assignment': 'off',
    },
  },
  
  // Svelte module files (.svelte.ts) — need Svelte rune globals
  {
    files: ['**/*.svelte.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: 'module',
        project: './tsconfig.json',
      },
      globals: {
        ...browserGlobals,
        ...svelteRuneGlobals,
      },
    },
    plugins: {
      '@typescript-eslint': tsPlugin,
      security,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...tsPlugin.configs.strict.rules,
      ...security.configs.recommended.rules,
      ...sharedTypeScriptRules,
    },
  },

  // TypeScript files (excluding .svelte.ts which is handled above)
  {
    files: ['**/*.ts'],
    ignores: ['**/*.svelte.ts'],
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
      security,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...tsPlugin.configs.strict.rules,
      ...security.configs.recommended.rules,
      ...sharedTypeScriptRules,
    },
  },

  // Playwright E2E test files
  {
    files: ['tests/**/*.ts', 'playwright.config.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: 'module',
        project: './tsconfig.playwright.json',
      },
      globals: {
        // Node.js globals for Playwright test environment
        global: 'readonly',
        process: 'readonly',
        Buffer: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
      },
    },
    plugins: {
      '@typescript-eslint': tsPlugin,
      playwright,
      security,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...tsPlugin.configs.strict.rules,
      ...playwright.configs['flat/recommended'].rules,
      // Security rules
      ...security.configs.recommended.rules,
      
      // E2E-specific Playwright rule overrides
      'playwright/no-conditional-in-test': 'off', // E2E tests need conditionals for optional UI elements
      'playwright/no-conditional-expect': 'off', // E2E tests need conditional expects for dynamic states
      'playwright/no-wait-for-timeout': 'off', // Sometimes necessary for timing-sensitive E2E scenarios

      ...sharedTypeScriptRules,
    },
  },

  // Playwright setup files - allow standalone expects
  {
    files: ['tests/**/*.setup.ts'],
    languageOptions: {
      parser: tsParser,
      parserOptions: {
        ecmaVersion: 2022,
        sourceType: 'module',
        project: './tsconfig.playwright.json',
      },
      globals: {
        global: 'readonly',
        process: 'readonly',
        Buffer: 'readonly',
        __dirname: 'readonly',
        __filename: 'readonly',
      },
    },
    plugins: {
      '@typescript-eslint': tsPlugin,
      playwright,
      security,
    },
    rules: {
      ...tsPlugin.configs.recommended.rules,
      ...playwright.configs['flat/recommended'].rules,
      
      // Setup-specific Playwright rule overrides
      'playwright/no-standalone-expect': 'off', // Allow standalone expect in setup
      'playwright/no-conditional-in-test': 'off', // E2E setup needs conditionals
      'playwright/no-conditional-expect': 'off', // E2E setup needs conditional expects
      'playwright/no-wait-for-timeout': 'off', // Sometimes necessary in setup

      ...sharedTypeScriptRules,
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
    plugins: {
      security,
    },
    rules: {
      // Security rules
      ...security.configs.recommended.rules,
      // General rules
      'no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': 'warn',
      'prefer-const': 'error',
      'no-var': 'error',
      'no-useless-assignment': 'off',
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
      'no-console': 'off',
    },
    languageOptions: {
      globals: {
        ...browserGlobals,
        ...vitest.environments.env.globals,
        // Additional test globals if needed
        global: 'readonly',
        render: 'readonly',
      },
    },
  },
  
  // Node.js scripts and config files
  {
    files: ['src/test/**/*.js', '*.config.js', '*.config.ts', 'test-*.js', 'debug-*.js', 'src/lib/i18n/generateTypes.ts'],
    languageOptions: {
      globals: {
        // Node.js specific globals only (no browser globals for Node scripts)
        global: 'readonly',
        process: 'readonly',
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
      'playwright-report/',
      'test-results/',
      'blob-report/',
    ],
  },
];