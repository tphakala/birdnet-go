import { t } from './store.svelte.js';
import type { MessageKey } from './types.js';

// Create message functions that match Paraglide's API. The target stays empty:
// the proxy synthesizes a function for every key on access.
const messageTarget: Partial<Record<MessageKey, () => string>> = {};
export const messages = new Proxy(messageTarget, {
  get(target, prop) {
    if (typeof prop === 'string') {
      return () => t(prop);
    }
    return undefined;
  },
}) as Record<MessageKey, () => string>;

// Shorter alias (matches current usage: m.hero_title())
export const m = messages;

// Individual message exports removed - use the messages proxy or t() directly
