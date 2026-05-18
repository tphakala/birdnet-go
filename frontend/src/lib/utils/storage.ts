const isBrowser = typeof window !== 'undefined';

/** Read a JSON-serialized value from localStorage with SSR safety and validation. */
export function getStoredValue<T>(
  key: string,
  defaultValue: T,
  validate?: (v: unknown) => v is T
): T {
  if (!isBrowser) return defaultValue;
  try {
    const raw = localStorage.getItem(key);
    if (raw === null) return defaultValue;
    const parsed: unknown = JSON.parse(raw);
    if (validate && !validate(parsed)) return defaultValue;
    return parsed as T;
  } catch {
    return defaultValue;
  }
}

/** Write a JSON-serialized value to localStorage with SSR safety. */
export function setStoredValue<T>(key: string, value: T): void {
  if (!isBrowser) return;
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // quota exceeded or private browsing
  }
}

/** Remove a key from localStorage with SSR safety. */
export function removeStoredValue(key: string): void {
  if (!isBrowser) return;
  try {
    localStorage.removeItem(key);
  } catch {
    // private browsing
  }
}
