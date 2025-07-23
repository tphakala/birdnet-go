// Export all i18n configuration and utilities
export * from './config';
export * from './utils';
export * from './store.svelte';

// Export generated types for compile-time validation
export type {
  TranslationKey,
  TranslationParams,
  GetParams,
  TranslateFunction,
} from './types.generated';
