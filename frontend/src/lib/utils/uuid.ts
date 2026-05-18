function fallbackId(): string {
  return Array.from({ length: 8 }, () => Math.floor(Math.random() * 36).toString(36)).join('');
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
