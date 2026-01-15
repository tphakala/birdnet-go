/**
 * Security utilities for safe object access and input validation
 * Helps prevent object injection, ReDoS, and path traversal vulnerabilities
 */

/**
 * PERFORMANCE NOTE: Security utilities should be used judiciously.
 *
 * Use these utilities when:
 * - Keys come from user input or external data
 * - Accessing properties on objects from API responses
 * - Keys are computed dynamically at runtime
 * - Working with untrusted data sources
 *
 * DON'T use these utilities when:
 * - Accessing typed constants (sizeClasses, variantClasses, etc.)
 * - Keys are string literals or typed enums
 * - TypeScript already provides compile-time safety
 * - Performance is critical and keys are known/trusted
 *
 * Example:
 * ✅ Good: safeGet(userInput, dynamicKey)
 * ❌ Avoid: safeGet(sizeClasses, 'sm') - use direct access instead
 */

/**
 * Check if a value is a plain object (not array, null, or other special objects)
 * Used for type validation in API data processing
 */
export function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return false;
  }

  // Check if the prototype is exactly Object.prototype or null
  const proto = Object.getPrototypeOf(value);
  return proto === null || proto === Object.prototype;
}

/**
 * Safely access object properties with validation
 * Prevents object injection by using hasOwnProperty check
 */

export function safeGet<T extends object, K extends keyof T>(
  obj: T,
  key: string | number | symbol,
  defaultValue: T[K]
): T[K];
// eslint-disable-next-line no-redeclare
export function safeGet<T extends object, K extends keyof T>(
  obj: T,
  key: string | number | symbol
): T[K] | undefined;
// eslint-disable-next-line no-redeclare
export function safeGet<T extends object, K extends keyof T>(
  obj: T,
  key: string | number | symbol,
  defaultValue?: T[K]
): T[K] | undefined {
  if (Object.prototype.hasOwnProperty.call(obj, key)) {
    return obj[key as K];
  }
  return defaultValue;
}

/**
 * Safe lookup with whitelist validation
 * Only allows access to predefined keys
 */
export function safeLookup<T>(
  lookupTable: Record<string, T>,
  key: string,
  allowedKeys: readonly string[],
  defaultValue?: T
): T | undefined {
  if (allowedKeys.includes(key)) {
    // eslint-disable-next-line security/detect-object-injection
    return lookupTable[key];
  }
  return defaultValue;
}

/**
 * Create a safe Map from an object for dynamic lookups
 * Maps are inherently safe from prototype pollution
 */

export function createSafeMap<T>(obj: Record<string, T>): Map<string, T>;
// eslint-disable-next-line no-redeclare
export function createSafeMap<V>(): Map<string, V>;
// eslint-disable-next-line no-redeclare
export function createSafeMap<V>(obj?: Record<string, V>): Map<string, V> {
  if (obj) {
    return new Map(Object.entries(obj) as [string, V][]);
  }
  return new Map<string, V>();
}

/**
 * Safe array index access with bounds checking
 */
export function safeArrayAccess<T>(arr: T[], index: number, defaultValue?: T): T | undefined {
  if (index >= 0 && index < arr.length) {
    // eslint-disable-next-line security/detect-object-injection
    return arr[index];
  }
  return defaultValue;
}

/**
 * Input validation with length limits to prevent ReDoS
 */
export function validateInput(input: string, maxLength: number, pattern?: RegExp): boolean {
  if (!input || input.length > maxLength) {
    return false;
  }
  return pattern ? pattern.test(input) : true;
}

/**
 * Safe regex execution with length protection
 * Prevents ReDoS attacks by limiting input length
 */
export function safeRegexTest(pattern: RegExp, input: string, maxLength = 1000): boolean {
  if (input.length > maxLength) {
    return false;
  }
  try {
    return pattern.test(input);
  } catch {
    return false;
  }
}

/**
 * CIDR validation without complex regex
 * Structured validation prevents ReDoS
 */
export function validateCIDR(cidr: string): boolean {
  if (!cidr || cidr.length > 18) return false; // max: xxx.xxx.xxx.xxx/xx

  const parts = cidr.split('/');
  if (parts.length !== 2) return false;

  const [ip, mask] = parts;
  const octets = ip.split('.');

  if (octets.length !== 4) return false;

  for (const octet of octets) {
    if (!/^\d{1,3}$/.test(octet)) return false;
    const num = parseInt(octet, 10);
    if (num > 255) return false;
  }

  if (!/^\d{1,2}$/.test(mask)) return false;
  const maskNum = parseInt(mask, 10);
  return maskNum >= 0 && maskNum <= 32;
}

/**
 * URL validation for specific protocols
 * Uses URL constructor for safe parsing
 */
export function validateProtocolURL(
  url: string,
  allowedProtocols: string[],
  maxLength = 2048
): boolean {
  if (!url || url.length > maxLength) return false;

  try {
    const parsed = new URL(url);
    return allowedProtocols.includes(parsed.protocol.slice(0, -1));
  } catch {
    return false;
  }
}

/**
 * Sanitize filename to prevent path traversal
 * Removes directory components and unsafe characters
 */
export function sanitizeFilename(filename: string): string {
  // Remove any path components
  const basename = filename.split(/[\\/]/).pop() ?? '';
  // Allow only safe characters
  return basename.replace(/[^a-zA-Z0-9._-]/g, '_');
}

/**
 * Create enum-based lookup helper for finite sets
 * Type-safe alternative to dynamic object access
 */
export function createEnumLookup<T>(enumObj: Record<string, T>): (key: string) => T | undefined {
  const validKeys = Object.keys(enumObj);
  return (key: string) => {
    if (validKeys.includes(key)) {
      // eslint-disable-next-line security/detect-object-injection
      return enumObj[key];
    }
    return undefined;
  };
}

/**
 * Safe switch-case replacement for object lookups
 * Use when you have a small, known set of keys
 */
export function safeSwitch<T>(key: string, cases: Record<string, T>, defaultValue: T): T {
  return safeGet(cases, key, defaultValue) ?? defaultValue;
}

/**
 * Safe object spreading that handles undefined/null values
 * Prevents "Spread types may only be created from object types" errors
 */
export function safeSpread<T extends object>(...objects: (T | undefined | null)[]): Partial<T> {
  return objects.reduce((result, obj) => {
    if (obj != null && typeof obj === 'object') {
      return { ...result, ...obj };
    }
    return result;
  }, {} as Partial<T>);
}

/**
 * Safe array spreading that handles undefined arrays
 * Prevents spreading undefined in array literals
 */
export function safeArraySpread<T>(...arrays: (T[] | undefined)[]): T[] {
  const result: T[] = [];
  for (const arr of arrays) {
    if (Array.isArray(arr)) {
      result.push(...arr);
    }
  }
  return result;
}

/**
 * Map wrapper that provides bracket-like access syntax
 * Provides type-safe alternative to Map.get() with fallback values
 */
export class SafeAccessMap<K, V> extends Map<K, V> {
  safeGet(key: K, defaultValue?: V): V | undefined {
    return this.get(key) ?? defaultValue;
  }

  hasError(key: K): boolean {
    return this.has(key);
  }

  getError(key: K): V | undefined {
    return this.get(key);
  }
}

/**
 * Convert NodeListOf<Element> to typed array for safe array access
 * Handles DOM API incompatibilities with array methods
 */
export function nodeListToArray<T extends Element>(nodeList: NodeListOf<Element>): T[] {
  return Array.from(nodeList) as T[];
}

/**
 * Safe DOM element access with type checking
 * Returns undefined if element doesn't exist or wrong type
 */
export function safeElementAccess<T extends HTMLElement>(
  elements: NodeListOf<Element> | HTMLElement[],
  index: number,
  elementType?: new () => T
): T | undefined {
  const arr = Array.isArray(elements) ? elements : Array.from(elements);
  const element = safeArrayAccess(arr, index);

  if (!element) return undefined;
  if (elementType && !(element instanceof elementType)) return undefined;

  return element as T;
}

/**
 * Safe number to string key conversion for Maps
 * Ensures consistent key types in Map operations
 */
export function numberToStringKey(key: number): string {
  return key.toString();
}

/**
 * Create a Map with number keys converted to strings
 * Handles index-based Map operations safely
 */
export function createIndexMap<V>(): Map<string, V> {
  return new Map<string, V>();
}

/**
 * Safe Map operations with number index conversion
 * Automatically converts number indices to string keys
 */
export class IndexMap<V> extends Map<string, V> {
  setByIndex(index: number, value: V): this {
    return this.set(numberToStringKey(index), value);
  }

  getByIndex(index: number): V | undefined {
    return this.get(numberToStringKey(index));
  }

  deleteByIndex(index: number): boolean {
    return this.delete(numberToStringKey(index));
  }

  hasByIndex(index: number): boolean {
    return this.has(numberToStringKey(index));
  }
}

/**
 * Safe property access with type guards
 * Returns undefined for null/undefined objects, typed result otherwise
 */
export function safePropertyAccess<T, K extends keyof T>(
  obj: T | null | undefined,
  key: K
): T[K] | undefined {
  if (obj == null) return undefined;
  // eslint-disable-next-line security/detect-object-injection
  return obj[key];
}

/**
 * Safe property access with fallback value
 * Returns fallback for null/undefined objects or missing properties
 */
export function safePropertyAccessWithFallback<T, K extends keyof T>(
  obj: T | null | undefined,
  key: K,
  fallback: T[K]
): T[K] {
  if (obj == null) return fallback;
  // eslint-disable-next-line security/detect-object-injection
  return obj[key] ?? fallback;
}

/**
 * Create type-safe default objects for common patterns
 * Prevents "missing properties" errors when providing defaults
 */
export function createTypedDefault<T>(defaults: T): T {
  return { ...defaults };
}

/**
 * Ensure object has required properties with fallbacks
 * Merges provided object with defaults, ensuring all required props exist
 */
export function ensureRequiredProperties<T extends object>(
  obj: Partial<T> | undefined | null,
  defaults: T
): T {
  if (obj == null) return { ...defaults };
  return { ...defaults, ...obj };
}

/**
 * Mask credentials in URLs for safe display
 * Uses URL object to handle all protocol schemes uniformly
 * Prevents credential exposure in UI for any URL type (RTSP, RTMP, HTTP, etc.)
 */
export function maskUrlCredentials(urlStr: string): string {
  // Length protection to prevent ReDoS
  if (!urlStr || urlStr.length > 2048) return urlStr;

  try {
    const urlObj = new URL(urlStr);
    if (urlObj.username || urlObj.password) {
      urlObj.username = '***';
      urlObj.password = '***';
      return urlObj.toString();
    }
    return urlStr;
  } catch {
    // Fallback for malformed URLs - use indexOf for safe credential detection
    const protoEnd = urlStr.indexOf('://');
    if (protoEnd === -1) return urlStr;

    const atIndex = urlStr.indexOf('@', protoEnd + 3);
    if (atIndex === -1) return urlStr;

    // Found credentials: protocol://...@host -> protocol://***:***@host
    const protocol = urlStr.slice(0, protoEnd + 3);
    const rest = urlStr.slice(atIndex);
    return protocol + '***:***' + rest;
  }
}

/**
 * Sanitize URL for comparison purposes (credentials masked)
 * Returns a normalized URL string suitable for equality checks
 */
export function sanitizeUrlForComparison(urlStr: string): string {
  return maskUrlCredentials(urlStr);
}
