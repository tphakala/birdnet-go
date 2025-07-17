/**
 * Utility functions for detecting changes in settings sections
 */

/**
 * Deep compare two objects to detect changes
 */
export function hasSettingsChanged(original: unknown, current: unknown): boolean {
  if (original === undefined || current === undefined) return false;
  return JSON.stringify(original) !== JSON.stringify(current);
}

/**
 * Extract a specific section from settings data for comparison
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
      result = (result as Record<string, unknown>)[key];
    } else {
      return undefined;
    }
  }

  return result as T;
}

/**
 * Check if a specific settings section has changes
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
 */
export function hasAnySectionChanged(
  originalData: unknown,
  currentData: unknown,
  sectionPaths: string[]
): boolean {
  return sectionPaths.some(path => hasSectionChanged(originalData, currentData, path));
}
