/**
 * Typed model catalog fixtures: complete objects with overridable fields, so a test states
 * only what it cares about.
 */

import type { CatalogEntry, DownloadProgress } from '$lib/types/models';
import { CHANNEL_STABLE } from '$lib/utils/variantSelection';

const ENTRY_TOTAL_BYTES = 1_000_000;
const PROGRESS_TOTAL_BYTES = 2_000_000;

/** A compatible, not installed, stable bird model; `overrides` replace any field. */
export function catalogEntry(overrides: Partial<CatalogEntry> = {}): CatalogEntry {
  return {
    id: 'test-bird',
    name: 'Test Bird Model',
    description: 'A test bird classifier',
    author: 'Tester',
    license: 'CC-BY-NC-4.0',
    commercialUse: false,
    category: 'bird',
    region: '',
    speciesCount: 100,
    version: '1.0',
    channel: CHANNEL_STABLE,
    installed: false,
    compatible: true,
    totalSizeBytes: ENTRY_TOTAL_BYTES,
    hasGeomodel: false,
    ...overrides,
  };
}

/** A download half way through the first of two files; `overrides` replace any field. */
export function downloadProgress(overrides: Partial<DownloadProgress> = {}): DownloadProgress {
  return {
    catalogId: 'test-bird',
    status: 'downloading',
    downloadedBytes: PROGRESS_TOTAL_BYTES / 2,
    totalBytes: PROGRESS_TOTAL_BYTES,
    currentFile: 1,
    totalFiles: 2,
    ...overrides,
  };
}
