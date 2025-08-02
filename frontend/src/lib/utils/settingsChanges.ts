/**
 * Utility functions for detecting changes in settings sections
 *
 * Performance optimized for Svelte 5 reactive proxies and deep object comparison.
 * Avoids JSON.stringify for better performance and proxy compatibility.
 */

import { safeGet } from './security';

/**
 * Deep compare two values to detect changes
 *
 * PERFORMANCE OPTIMIZATIONS:
 * - Early returns for common cases (same reference, null/undefined)
 * - Type checking before recursive comparison
 * - Efficient array and object iteration
 * - No JSON.stringify overhead
 * - Handles circular references gracefully
 *
 * @param a First value to compare
 * @param b Second value to compare
 * @param seen WeakSet to track circular references
 * @returns true if values are deeply equal, false otherwise
 */
export function deepEqual(a: unknown, b: unknown, seen = new WeakSet()): boolean {
  // Same reference or both null/undefined
  if (a === b) return true;

  // One is null/undefined but not both
  if (a == null || b == null) return false;

  // Different types
  const typeA = typeof a;
  const typeB = typeof b;
  if (typeA !== typeB) return false;

  // Primitives (string, number, boolean)
  if (typeA !== 'object') return false;

  // For objects and arrays
  const objA = a as Record<string, unknown>;
  const objB = b as Record<string, unknown>;

  // Check for circular references
  if (seen.has(objA) || seen.has(objB)) return objA === objB;
  seen.add(objA);
  seen.add(objB);

  // Arrays
  if (Array.isArray(objA)) {
    if (!Array.isArray(objB)) return false;
    if (objA.length !== objB.length) return false;

    for (let i = 0; i < objA.length; i++) {
      if (!deepEqual(objA[i], objB[i], seen)) return false;
    }
    return true;
  }

  // Objects
  const keysA = Object.keys(objA);
  const keysB = Object.keys(objB);

  if (keysA.length !== keysB.length) return false;

  for (const key of keysA) {
    if (!keysB.includes(key)) return false;
    if (!deepEqual(objA[key], objB[key], seen)) return false;
  }

  return true;
}

/**
 * Detect if settings have changed using optimized deep comparison
 *
 * @param original Original settings value
 * @param current Current settings value
 * @returns true if settings have changed, false otherwise
 */
export function hasSettingsChanged(original: unknown, current: unknown): boolean {
  if (original === undefined || current === undefined) return false;
  return !deepEqual(original, current);
}

/**
 * Extract a specific section from settings data for comparison
 *
 * @param data Settings data object
 * @param sectionPath Dot-separated path to section (e.g., "audio.capture")
 * @returns Extracted section or undefined if not found
 */
export function extractSettingsSection<T>(data: unknown, sectionPath: string): T | undefined {
  if (!data || typeof data !== 'object') return undefined;

  // Handle empty path - return the data itself
  if (!sectionPath || sectionPath.trim() === '') {
    return data as T;
  }

  const keys = sectionPath.split('.');
  let result: unknown = data;

  for (const key of keys) {
    if (result && typeof result === 'object' && key in result) {
      result = safeGet(result as Record<string, unknown>, key);
    } else {
      return undefined;
    }
  }

  return result as T;
}

/**
 * Check if a specific settings section has changes
 *
 * @param originalData Original settings data
 * @param currentData Current settings data
 * @param sectionPath Dot-separated path to section
 * @returns true if section has changed, false otherwise
 */
export function hasSectionChanged<T>(
  originalData: unknown,
  currentData: unknown,
  sectionPath: string
): boolean {
  const originalSection = extractSettingsSection<T>(originalData, sectionPath);
  const currentSection = extractSettingsSection<T>(currentData, sectionPath);

  return hasSettingsChanged(originalSection, currentSection);
}

/**
 * Check if any subsection within a section has changes
 *
 * @param originalData Original settings data
 * @param currentData Current settings data
 * @param sectionPaths Array of dot-separated paths to check
 * @returns true if any section has changed, false otherwise
 */
export function hasAnySectionChanged(
  originalData: unknown,
  currentData: unknown,
  sectionPaths: string[]
): boolean {
  return sectionPaths.some(path => hasSectionChanged(originalData, currentData, path));
}

/**
 * Create a snapshot of Svelte proxy state for comparison
 * Use this when comparing Svelte reactive proxies to avoid proxy comparison issues
 *
 * @param value Value to snapshot (may be a proxy)
 * @returns Plain object/array snapshot
 */
export function createSnapshot<T>(value: T): T {
  if (value == null || typeof value !== 'object') return value;

  if (Array.isArray(value)) {
    return value.map(item => createSnapshot(item)) as T;
  }

  const snapshot: Record<string, unknown> = {};
  for (const key in value) {
    if (Object.prototype.hasOwnProperty.call(value, key)) {
      snapshot[key] = createSnapshot((value as Record<string, unknown>)[key]);
    }
  }

  return snapshot as T;
}
