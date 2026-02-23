/**
 * Integration Tests: Duplicate Key Validation with Real Database Data
 *
 * These tests run in a real browser against a real BirdNET-Go backend
 * to validate that API responses won't cause duplicate key errors when
 * rendered by Svelte components.
 *
 * Svelte 5 throws `each_key_duplicate` runtime errors when {#each} blocks
 * encounter non-unique keys. These tests catch data-level issues that
 * synthetic unit tests cannot — for example, the v2 database schema
 * producing rows with duplicate scientific names or detection IDs.
 *
 * Prerequisites:
 *   - Backend running on http://localhost:8080
 *   - Start with: task integration-backend
 *
 * Usage:
 *   npm run test:integration
 */

import { describe, expect, it } from 'vitest';
import { apiCall } from './integration-setup';
import { getLocalDateString } from '$lib/utils/date';

// ============================================================================
// Helper: Check array for duplicate values in a specific field
// ============================================================================

/**
 * Finds duplicate values for a given key in an array of objects.
 * Returns an array of { value, indexes } for each duplicate found.
 */
function findDuplicateKeys<T>(
  items: T[],
  keyFn: (item: T) => string | number | undefined
): Array<{ value: string | number; indexes: number[] }> {
  const seen = new Map<string | number, number[]>();

  items.forEach((item, index) => {
    const key = keyFn(item);
    if (key === undefined) return;

    const existing = seen.get(key);
    if (existing) {
      existing.push(index);
    } else {
      seen.set(key, [index]);
    }
  });

  return Array.from(seen.entries())
    .filter(([, indexes]) => indexes.length > 1)
    .map(([value, indexes]) => ({ value, indexes }));
}

// ============================================================================
// Daily Species Summary — DailySummaryCard uses (item.scientific_name) as key
// ============================================================================

describe('Duplicate Keys: Daily Species Summary', () => {
  it('daily species summary has unique scientific_name keys', async () => {
    // DailySummaryCard.svelte:1029 — {#each sortedData as item (item.scientific_name)}
    // If the v2 schema returns multiple rows for the same species on a given day,
    // the component will crash with each_key_duplicate
    const today = getLocalDateString();
    const response = await apiCall(`/analytics/species/daily?date=${today}&limit=100`);

    if (!response.ok) {
      // No data for today is fine — skip test
      console.log(`No daily summary for ${today} (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();

    // Response may be an array or wrapped in an object
    const species: Array<{ scientific_name: string }> = Array.isArray(data)
      ? data
      : (data.species ?? data.data ?? []);

    if (species.length === 0) {
      console.log('No species data for today, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(species, item => item.scientific_name);

    expect(duplicates).toEqual([]);
  });

  it('daily species summary across multiple dates has unique keys per date', async () => {
    // Test the last 7 days to catch schema issues
    const dates: string[] = [];
    for (let i = 0; i < 7; i++) {
      const d = new Date();
      d.setDate(d.getDate() - i);
      dates.push(getLocalDateString(d));
    }

    for (const date of dates) {
      const response = await apiCall(`/analytics/species/daily?date=${date}&limit=200`);

      if (!response.ok) continue;

      const data = await response.json();
      const species: Array<{ scientific_name: string }> = Array.isArray(data)
        ? data
        : (data.species ?? data.data ?? []);

      if (species.length === 0) continue;

      const duplicates = findDuplicateKeys(species, item => item.scientific_name);

      expect(
        duplicates,
        `Duplicate scientific_name in daily summary for ${date}: ${JSON.stringify(duplicates)}`
      ).toEqual([]);
    }
  });
});

// ============================================================================
// Species Summary — Species.svelte uses (species.scientific_name) as key
// ============================================================================

describe('Duplicate Keys: Species Summary', () => {
  it('species summary has unique scientific_name keys', async () => {
    // Species.svelte:509,518,539,593 — {#each filteredSpecies as species (species.scientific_name)}
    const today = getLocalDateString();
    const weekAgo = getLocalDateString(new Date(Date.now() - 7 * 24 * 60 * 60 * 1000));

    const response = await apiCall(
      `/analytics/species/summary?start_date=${weekAgo}&end_date=${today}`
    );

    if (!response.ok) {
      console.log(`Species summary not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const species: Array<{ scientific_name: string }> = Array.isArray(data)
      ? data
      : (data.species ?? data.data ?? []);

    if (species.length === 0) {
      console.log('No species summary data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(species, item => item.scientific_name);

    expect(
      duplicates,
      `Duplicate scientific_name in species summary: ${JSON.stringify(duplicates)}`
    ).toEqual([]);
  });
});

// ============================================================================
// Detections — DetectionsList, DetectionCardGrid use (detection.id) as key
// ============================================================================

describe('Duplicate Keys: Detections', () => {
  it('detections list has unique id keys', async () => {
    // DetectionsList.svelte:345 — {#each data.notes as detection (detection.id)}
    // DetectionCardGrid.svelte:275 — {#each data.slice(0, selectedLimit) as detection (detection.id)}
    const response = await apiCall('/detections?limit=500');

    if (!response.ok) {
      console.log(`Detections not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const detections: Array<{ id: string | number }> = Array.isArray(data)
      ? data
      : (data.notes ?? data.detections ?? data.data ?? []);

    if (detections.length === 0) {
      console.log('No detection data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(detections, item => item.id);

    expect(duplicates, `Duplicate detection IDs: ${JSON.stringify(duplicates)}`).toEqual([]);
  });

  it('recent detections have unique id keys', async () => {
    // Analytics.svelte:1348 — {#each recentDetections as detection, index (detection.id ?? index)}
    const response = await apiCall('/detections/recent?limit=50');

    if (!response.ok) {
      console.log(`Recent detections not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const detections: Array<{ id: string | number }> = Array.isArray(data)
      ? data
      : (data.notes ?? data.detections ?? data.data ?? []);

    if (detections.length === 0) {
      console.log('No recent detection data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(detections, item => item.id);

    expect(duplicates, `Duplicate recent detection IDs: ${JSON.stringify(duplicates)}`).toEqual([]);
  });
});

// ============================================================================
// Species List — SpeciesSettingsPage uses (species) string as key
// ============================================================================

describe('Duplicate Keys: Species Configuration', () => {
  it('settings include/exclude species lists have unique entries', async () => {
    // SpeciesSettingsPage.svelte:1286 — {#each settings.include as species (species)}
    // SpeciesSettingsPage.svelte:1338 — {#each settings.exclude as species (species)}
    // These iterate string arrays using the string value as key
    const response = await apiCall('/settings');

    if (!response.ok) {
      console.log(`Settings not available (status ${response.status}), skipping`);
      return;
    }

    const settings = await response.json();

    // Check include list (empty lists trivially pass: Set([]).size === 0)
    const includeList: string[] = settings?.realtime?.species?.include ?? [];
    const includeSet = new Set(includeList);
    expect(
      includeList.length,
      `Duplicate species in include list: ${JSON.stringify(includeList.filter((s: string, i: number) => includeList.indexOf(s) !== i))}`
    ).toBe(includeSet.size);

    // Check exclude list
    const excludeList: string[] = settings?.realtime?.species?.exclude ?? [];
    const excludeSet = new Set(excludeList);
    expect(
      excludeList.length,
      `Duplicate species in exclude list: ${JSON.stringify(excludeList.filter((s: string, i: number) => excludeList.indexOf(s) !== i))}`
    ).toBe(excludeSet.size);
  });

  it('range filter species have unique scientificName keys', async () => {
    // MainSettingsPage.svelte:2977 — {#each rangeFilterState.species as species (species.scientificName)}
    const response = await apiCall('/range/species/list');

    if (!response.ok) {
      // Range filter may not be configured
      console.log(`Range filter species not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const species: Array<{ scientificName?: string; scientific_name?: string }> = Array.isArray(
      data
    )
      ? data
      : (data.species ?? data.data ?? []);

    if (species.length === 0) {
      console.log('No range filter species data, skipping');
      return;
    }

    // The key field could be scientificName or scientific_name depending on the API
    const duplicates = findDuplicateKeys(
      species,
      item => item.scientificName ?? item.scientific_name
    );

    expect(
      duplicates,
      `Duplicate scientificName in range filter species: ${JSON.stringify(duplicates)}`
    ).toEqual([]);
  });
});

// ============================================================================
// Audio Devices — AudioSettingsPage uses device values in SelectDropdown
// ============================================================================

describe('Duplicate Keys: Audio Devices', () => {
  it('audio devices have unique id values', async () => {
    // AudioSettingsPage derives audioSourceOptions from device data
    // SelectDropdown.svelte uses (option.value) as key (now fixed to composite key)
    // But verifying data uniqueness is still valuable for correct behavior
    const response = await apiCall('/system/audio/devices');

    if (!response.ok) {
      console.log(`Audio devices not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const devices: Array<{ id: string; name: string }> = Array.isArray(data)
      ? data
      : (data.devices ?? data.data ?? []);

    if (devices.length === 0) {
      console.log('No audio devices found, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(devices, item => item.id);

    expect(duplicates, `Audio devices with duplicate IDs: ${JSON.stringify(duplicates)}`).toEqual(
      []
    );
  });
});

// ============================================================================
// Dynamic Thresholds — DynamicThresholdTab uses (threshold.speciesName)
// ============================================================================

describe('Duplicate Keys: Dynamic Thresholds', () => {
  it('active species have unique scientificName keys', async () => {
    // SpeciesSettingsPage.svelte:1188 — {#each filteredActiveSpecies as species (species.scientificName)}
    // DynamicThresholdTab.svelte:374 — {#each filteredThresholds as threshold (threshold.speciesName)}
    const response = await apiCall('/species');

    if (!response.ok) {
      console.log(`Species endpoint not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const species: Array<{ scientific_name?: string; scientificName?: string }> = Array.isArray(
      data
    )
      ? data
      : (data.species ?? data.data ?? []);

    if (species.length === 0) {
      console.log('No species data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(
      species,
      item => item.scientific_name ?? item.scientificName
    );

    expect(
      duplicates,
      `Duplicate species in species list: ${JSON.stringify(duplicates.slice(0, 5))}${duplicates.length > 5 ? `... and ${duplicates.length - 5} more` : ''}`
    ).toEqual([]);
  });
});

// ============================================================================
// All Species Labels — used by SpeciesManager autocomplete
// ============================================================================

describe('Duplicate Keys: All Species Labels', () => {
  it('species label list has no duplicates', async () => {
    // SpeciesManager predictions come from species/all or species/predictions
    // If the label list itself has duplicates, the predictions dropdown could
    // return duplicate values
    const response = await apiCall('/species/all');

    if (!response.ok) {
      console.log(`Species/all not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const labels: string[] = Array.isArray(data)
      ? data
      : (data.labels ?? data.species ?? data.data ?? []);

    if (labels.length === 0) {
      console.log('No species labels, skipping');
      return;
    }

    const labelSet = new Set(labels);
    const duplicateCount = labels.length - labelSet.size;

    expect(
      duplicateCount,
      `Found ${duplicateCount} duplicate labels in species/all (total: ${labels.length})`
    ).toBe(0);
  });
});

// ============================================================================
// Hourly Distribution — used by Analytics time charts
// ============================================================================

describe('Duplicate Keys: Hourly Distribution', () => {
  it('hourly distribution has unique time slots', async () => {
    const today = getLocalDateString();
    const weekAgo = getLocalDateString(new Date(Date.now() - 7 * 24 * 60 * 60 * 1000));

    const response = await apiCall(
      `/analytics/time/distribution/hourly?start_date=${weekAgo}&end_date=${today}`
    );

    if (!response.ok) {
      console.log(`Hourly distribution not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const distribution: Array<{ hour: number }> = Array.isArray(data)
      ? data
      : (data.distribution ?? data.data ?? []);

    if (distribution.length === 0) {
      console.log('No hourly distribution data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(distribution, item => item.hour);

    expect(
      duplicates,
      `Duplicate hours in hourly distribution: ${JSON.stringify(duplicates)}`
    ).toEqual([]);
  });
});

// ============================================================================
// New Species Detections — Analytics page
// ============================================================================

describe('Duplicate Keys: New Species Detections', () => {
  it('new species detections have unique identifiers', async () => {
    const today = getLocalDateString();
    const monthAgo = getLocalDateString(new Date(Date.now() - 30 * 24 * 60 * 60 * 1000));

    const response = await apiCall(
      `/analytics/species/detections/new?start_date=${monthAgo}&end_date=${today}`
    );

    if (!response.ok) {
      console.log(`New species not available (status ${response.status}), skipping`);
      return;
    }

    const data = await response.json();
    const newSpecies: Array<{ scientific_name?: string; scientificName?: string }> = Array.isArray(
      data
    )
      ? data
      : (data.species ?? data.data ?? []);

    if (newSpecies.length === 0) {
      console.log('No new species data, skipping');
      return;
    }

    const duplicates = findDuplicateKeys(
      newSpecies,
      item => item.scientific_name ?? item.scientificName
    );

    expect(duplicates, `Duplicate new species: ${JSON.stringify(duplicates)}`).toEqual([]);
  });
});
