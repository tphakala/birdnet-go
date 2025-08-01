/**
 * Simple LRU (Least Recently Used) cache implementation
 * @template K - The type of the cache keys
 * @template V - The type of the cache values
 */
export class LRUCache<K, V> {
  private readonly cache: Map<K, V> = new Map();
  private readonly maxSize: number;

  constructor(maxSize: number) {
    this.maxSize = maxSize;
  }

  get(key: K): V | undefined {
    if (!this.cache.has(key)) return undefined;

    // Move to end (most recently used)
    // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- Key existence already verified above
    const value = this.cache.get(key)!;
    this.cache.delete(key);
    this.cache.set(key, value);
    return value;
  }

  set(key: K, value: V): void {
    // If key exists, delete it to update position
    if (this.cache.has(key)) {
      this.cache.delete(key);
    } else if (this.cache.size >= this.maxSize) {
      // Remove least recently used (first item)
      const firstKeyResult = this.cache.keys().next();
      if (!firstKeyResult.done && firstKeyResult.value !== undefined) {
        this.cache.delete(firstKeyResult.value);
      }
    }

    // Add to end (most recently used)
    this.cache.set(key, value);
  }

  has(key: K): boolean {
    return this.cache.has(key);
  }

  clear(): void {
    this.cache.clear();
  }

  get size(): number {
    return this.cache.size;
  }
}
