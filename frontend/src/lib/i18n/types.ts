// Type definitions for i18n
import type enMessages from '../../../static/messages/en.json';

// Derive message keys from English messages
export type MessageKey = keyof typeof enMessages;

// Type-safe translation function
export type TFunction = (key: string, params?: Record<string, unknown>) => string;

// Re-export for convenience
export type { Locale } from './config.js';

// Helper type to get nested keys with dot notation
type NestedKeyOf<T> = T extends object
  ? {
      [K in keyof T]: K extends string
        ? T[K] extends object
          ? `${K}.${NestedKeyOf<T[K]>}`
          : K
        : never;
    }[keyof T]
  : never;

// Type for all possible message keys with dot notation
export type DottedMessageKey = NestedKeyOf<typeof enMessages>;
