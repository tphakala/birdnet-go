import { t } from './store.svelte.js';
import type { MessageKey } from './types.js';

// Create message functions that match Paraglide's API
export const messages = new Proxy({} as Record<MessageKey, () => string>, {
  get(target, prop) {
    if (typeof prop === 'string') {
      return () => t(prop as MessageKey);
    }
    return undefined;
  },
});

// Shorter alias (matches current usage: m.hero_title())
export const m = messages;

// Individual message exports removed - use the messages proxy or t() directly
