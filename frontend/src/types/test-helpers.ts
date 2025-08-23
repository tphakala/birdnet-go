/**
 * Shared test helpers and utilities
 *
 * This file contains common test utilities that can be reused across multiple test files
 * to ensure consistency and reduce code duplication.
 */

import type { SettingsFormData } from '$lib/stores/settings';

/**
 * Creates a complete empty settings object for testing purposes
 *
 * This helper provides a full SettingsFormData object with all required properties
 * initialized to sensible default values for testing. It ensures that TypeScript
 * interfaces are satisfied and provides a consistent baseline for test scenarios.
 *
 * @returns Complete SettingsFormData object with default values
 */
export function createEmptySettings(): SettingsFormData {
  return {
    main: {
      name: '',
    },
    birdnet: {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.3,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 0,
      longitude: 0,
      rangeFilter: {
        threshold: 0.03,
        speciesCount: null,
        species: [],
      },
    },
    realtime: {
      species: {
        include: [],
        exclude: [],
        config: {},
      },
    },
  };
}
