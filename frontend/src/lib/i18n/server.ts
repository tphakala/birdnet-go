import type { Locale } from './config.js';
import { DEFAULT_LOCALE } from './config.js';

// Server-side locale storage (replaces overwriteGetLocale)
let serverLocale: Locale = DEFAULT_LOCALE;

export function setServerLocale(locale: Locale): void {
  serverLocale = locale;
}

export function getServerLocale(): Locale {
  return serverLocale;
}

// Server-side message loading
const messageCache = new Map<Locale, Record<string, string>>();

export async function loadServerMessages(locale: Locale): Promise<Record<string, string>> {
  if (!messageCache.has(locale)) {
    try {
      // Use Node.js fs to read from static directory
      const fs = await import('fs');
      const path = await import('path');
      // eslint-disable-next-line no-undef
      const filePath = path.join(process.cwd(), 'static', 'messages', `${locale}.json`);
      const content = fs.readFileSync(filePath, 'utf-8');
      const messages = JSON.parse(content);
      messageCache.set(locale, messages);
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error(`Failed to load messages for ${locale}:`, error);
      messageCache.set(locale, {});
    }
  }

  // eslint-disable-next-line @typescript-eslint/no-non-null-assertion
  return messageCache.get(locale)!;
}

// Server-side translation
export async function serverT(
  locale: Locale,
  key: string,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  params?: Record<string, any>,
): Promise<string> {
  const messages = await loadServerMessages(locale);
  // eslint-disable-next-line security/detect-object-injection
  const message = messages[key] || key;

  if (!params) return message;

  // eslint-disable-next-line security/detect-object-injection, @typescript-eslint/prefer-nullish-coalescing
  return message.replace(/\{(\w+)\}/g, (_, param) => params[param]?.toString() || `{${param}}`);
}
