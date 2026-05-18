function fallbackId(): string {
  return Math.random().toString(36).slice(2, 10);
}

export function generateId(prefix?: string): string {
  let id: string;
  try {
    id = crypto.randomUUID().slice(0, 8);
  } catch {
    id = fallbackId();
  }
  return prefix ? `${prefix}-${id}` : id;
}
