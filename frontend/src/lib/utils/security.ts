/**
 * Security utilities for safe object access and input validation
 * Helps prevent object injection, ReDoS, and path traversal vulnerabilities
 */

/**
 * Safely access object properties with validation
 * Prevents object injection by using hasOwnProperty check
 */
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
export function createSafeMap<T>(obj: Record<string, T>): Map<string, T> {
  return new Map(Object.entries(obj));
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
