export function generateSessionId(): string {
  try {
    return crypto.randomUUID();
  } catch {
    return `fallback-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  }
}
