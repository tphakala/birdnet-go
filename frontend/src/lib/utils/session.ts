/**
 * Generate a unique session ID for per-tab/component tracking.
 * Uses crypto.randomUUID() when available, with a fallback for
 * environments without Web Crypto API support.
 */
export function generateSessionId(): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return crypto.randomUUID();
  }
  return `fallback-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}
