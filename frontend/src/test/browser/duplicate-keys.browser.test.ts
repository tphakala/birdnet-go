/**
 * Browser Mode Tests: Duplicate Key Detection in {#each} Blocks
 *
 * These tests run in a real browser via Playwright to catch Svelte 5's
 * runtime `each_key_duplicate` errors that the compiler cannot detect.
 *
 * In Svelte 5 dev mode, duplicate keys in {#each} blocks throw an error:
 *   "Keyed each block has duplicate key `<key>` at indexes N and M"
 * This is a real runtime crash, not just a warning. These errors only
 * surface with real data at runtime, making them invisible to static
 * analysis and jsdom tests.
 *
 * Test strategy:
 * - Render thin wrapper components that reproduce each problematic {#each} pattern
 * - Feed them data that could contain duplicate key values
 * - Tests PASS when rendering succeeds without Svelte errors
 * - Tests FAIL when Svelte throws each_key_duplicate errors
 *
 * Related components and their issues:
 * - SpeciesManager.svelte:272    → {#each predictions as prediction (prediction)}
 * - SpeciesManager.svelte:295    → {#each displaySpecies as item, index (item)}
 * - SubnetInput.svelte:179       → {#each subnets as subnet, index (subnet)}
 * - SelectDropdown.svelte:477    → {#each options as option (option.value)}
 * - SelectField.svelte:103       → {#each options as option (option.value)}
 * - SpeciesSelector.svelte:413   → {#each filteredSpecies as group (group.category)}
 * - RTSPUrlInput.svelte:116      → {#each urls as url, index (index)}
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page } from 'vitest/browser';

import StringListKey from './wrappers/StringListKey.svelte';
import OptionValueKey from './wrappers/OptionValueKey.svelte';
import GroupCategoryKey from './wrappers/GroupCategoryKey.svelte';
import IndexKey from './wrappers/IndexKey.svelte';
import IndexKeyFixed from './wrappers/IndexKeyFixed.svelte';

// ============================================================================
// Pattern: String value as key — {#each items as item (item)}
// Affected: SpeciesManager predictions, SpeciesManager displaySpecies, SubnetInput
// ============================================================================

describe('Duplicate Key: String value as key — (item)', () => {
  it('renders unique string items without error', async () => {
    render(StringListKey, {
      items: ['Parus major', 'Turdus merula', 'Erithacus rubecula'],
    });

    await expect.element(page.getByTestId('string-list')).toBeVisible();

    const items = page.getByTestId('list-item');
    await expect.element(items.nth(0)).toHaveTextContent('Parus major');
    await expect.element(items.nth(1)).toHaveTextContent('Turdus merula');
    await expect.element(items.nth(2)).toHaveTextContent('Erithacus rubecula');
  });

  it('throws on duplicate string items (SpeciesManager predictions bug)', async () => {
    // Reproduces SpeciesManager.svelte:272 — {#each predictions as prediction (prediction)}
    // When the API returns the same species twice in predictions,
    // Svelte throws each_key_duplicate at runtime
    expect(() => {
      render(StringListKey, {
        items: ['Parus major', 'Turdus merula', 'Parus major'],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('throws on duplicate subnet strings (SubnetInput bug)', async () => {
    // Reproduces SubnetInput.svelte:179 — {#each subnets as subnet, index (subnet)}
    // When the subnets array has identical CIDR blocks
    expect(() => {
      render(StringListKey, {
        items: ['192.168.1.0/24', '10.0.0.0/8', '192.168.1.0/24'],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('renders empty list without error', async () => {
    render(StringListKey, { items: [] });
    // Empty <ul> renders but may not be "visible" (no height) — just check it's in the DOM
    await expect.element(page.getByTestId('string-list')).toBeInTheDocument();
  });

  it('renders single item without error', async () => {
    render(StringListKey, { items: ['Parus major'] });
    await expect.element(page.getByTestId('string-list')).toBeVisible();
  });
});

// ============================================================================
// Pattern: Object property as key — {#each options as option (option.value)}
// Affected: SelectDropdown, SelectField
// ============================================================================

describe('Duplicate Key: Object property as key — (option.value)', () => {
  it('renders unique option values without error', async () => {
    render(OptionValueKey, {
      options: [
        { value: 'wav', label: 'WAV' },
        { value: 'mp3', label: 'MP3' },
        { value: 'flac', label: 'FLAC' },
      ],
    });

    await expect.element(page.getByTestId('option-select')).toBeVisible();
  });

  it('throws on duplicate option values (SelectDropdown/SelectField bug)', async () => {
    // Reproduces SelectDropdown.svelte:477 and SelectField.svelte:103
    // When two options share the same value (e.g., multiple audio devices
    // reporting the same ID, or a device list with duplicate "default" entries)
    expect(() => {
      render(OptionValueKey, {
        options: [
          { value: 'default', label: 'System Default' },
          { value: 'hdmi', label: 'HDMI Output' },
          { value: 'default', label: 'Default (Speakers)' },
        ],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('throws when all options have the same value', async () => {
    expect(() => {
      render(OptionValueKey, {
        options: [
          { value: 'true', label: 'Yes' },
          { value: 'true', label: 'Enabled' },
          { value: 'true', label: 'On' },
        ],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });
});

// ============================================================================
// Pattern: Group category as key — {#each groups as group (group.category)}
// Affected: SpeciesSelector
// ============================================================================

describe('Duplicate Key: Group category as key — (group.category)', () => {
  it('renders unique category names without error', async () => {
    render(GroupCategoryKey, {
      groups: [
        {
          category: 'songbirds',
          items: [{ id: '1', name: 'Robin' }],
        },
        {
          category: 'waterfowl',
          items: [{ id: '2', name: 'Mallard' }],
        },
        {
          category: 'raptors',
          items: [{ id: '3', name: 'Eagle' }],
        },
      ],
    });

    await expect.element(page.getByTestId('group-list')).toBeVisible();
  });

  it('throws on duplicate category names (SpeciesSelector bug)', async () => {
    // Reproduces SpeciesSelector.svelte:413 — {#each filteredSpecies as group (group.category)}
    // When filteredSpecies contains multiple groups with the same category
    // (e.g., when filtering produces duplicate category groupings)
    expect(() => {
      render(GroupCategoryKey, {
        groups: [
          {
            category: 'songbirds',
            items: [{ id: '1', name: 'Robin' }],
          },
          {
            category: 'waterfowl',
            items: [{ id: '2', name: 'Mallard' }],
          },
          {
            category: 'songbirds',
            items: [{ id: '3', name: 'Wren' }],
          },
        ],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('throws on empty string categories (uncategorized fallback)', async () => {
    // When uncategorized species all get category: '' they produce duplicate keys
    expect(() => {
      render(GroupCategoryKey, {
        groups: [
          {
            category: '',
            items: [{ id: '1', name: 'Unknown Bird A' }],
          },
          {
            category: '',
            items: [{ id: '2', name: 'Unknown Bird B' }],
          },
        ],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });
});

// ============================================================================
// Pattern: Index as key — {#each items as item, index (index)}
// Affected: RTSPUrlInput
// ============================================================================

describe('Index as Key: DOM recycling issue — (index)', () => {
  it('renders all items correctly with index keys', async () => {
    // Index keys don't cause duplicate key errors (indices are unique)
    // but they cause incorrect DOM recycling on remove/reorder
    render(IndexKey, {
      items: ['rtsp://cam1/stream', 'rtsp://cam2/stream', 'rtsp://cam3/stream'],
    });

    await expect.element(page.getByTestId('index-key-list')).toBeVisible();

    const inputs = page.getByTestId('item-input');
    await expect.element(inputs.nth(0)).toHaveValue('rtsp://cam1/stream');
    await expect.element(inputs.nth(1)).toHaveValue('rtsp://cam2/stream');
    await expect.element(inputs.nth(2)).toHaveValue('rtsp://cam3/stream');
  });

  it('index key: rerender after middle removal shows correct items', async () => {
    // When using (index) as key and removing the middle item:
    // - Svelte sees keys [0, 1, 2] → [0, 1]
    // - It keeps DOM for keys 0 and 1, removes key 2
    // - DOM for key 1 still has old value (cam2) but data says cam3
    // This tests that rerender with the correct data works
    const { rerender } = render(IndexKey, {
      items: ['rtsp://cam1/stream', 'rtsp://cam2/stream', 'rtsp://cam3/stream'],
    });

    await expect.element(page.getByTestId('index-key-list')).toBeVisible();

    // Simulate removing middle item and rerendering
    await rerender({ items: ['rtsp://cam1/stream', 'rtsp://cam3/stream'] });

    const inputs = page.getByTestId('item-input');
    await expect.element(inputs.nth(0)).toHaveValue('rtsp://cam1/stream');
    await expect.element(inputs.nth(1)).toHaveValue('rtsp://cam3/stream');
  });

  it('value key: rerender after middle removal shows correct items', async () => {
    // The fixed version uses (item) instead of (index) as key
    // This correctly associates DOM elements with their data values
    const { rerender } = render(IndexKeyFixed, {
      items: ['rtsp://cam1/stream', 'rtsp://cam2/stream', 'rtsp://cam3/stream'],
    });

    await expect.element(page.getByTestId('index-key-list')).toBeVisible();

    const inputs = page.getByTestId('item-input');
    await expect.element(inputs.nth(0)).toHaveValue('rtsp://cam1/stream');
    await expect.element(inputs.nth(1)).toHaveValue('rtsp://cam2/stream');
    await expect.element(inputs.nth(2)).toHaveValue('rtsp://cam3/stream');

    // Rerender without middle item
    await rerender({ items: ['rtsp://cam1/stream', 'rtsp://cam3/stream'] });

    const updatedInputs = page.getByTestId('item-input');
    await expect.element(updatedInputs.nth(0)).toHaveValue('rtsp://cam1/stream');
    await expect.element(updatedInputs.nth(1)).toHaveValue('rtsp://cam3/stream');
  });
});

// ============================================================================
// Edge Cases: Boundary conditions that can trigger duplicate keys
// ============================================================================

describe('Duplicate Key Edge Cases', () => {
  it('case-different strings are unique keys (no error)', async () => {
    // Keys are case-sensitive, so "parus major" and "Parus major" are different
    render(StringListKey, {
      items: ['Parus major', 'parus major', 'PARUS MAJOR'],
    });

    await expect.element(page.getByTestId('string-list')).toBeVisible();
  });

  it('throws on empty string duplicate keys', async () => {
    // Empty strings as keys will collide
    expect(() => {
      render(StringListKey, {
        items: ['', 'valid', ''],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('throws on empty string duplicate option values', async () => {
    // Options where value is empty string can duplicate
    expect(() => {
      render(OptionValueKey, {
        options: [
          { value: '', label: 'None' },
          { value: 'valid', label: 'Valid' },
          { value: '', label: 'Unset' },
        ],
      });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });

  it('handles large list with unique items without error', async () => {
    // Ensure no false positives with many items
    const items = Array.from({ length: 100 }, (_, i) => `Species ${i}`);

    render(StringListKey, { items });

    await expect.element(page.getByTestId('string-list')).toBeVisible();
  });

  it('throws on large list with a single duplicate', async () => {
    // Even one duplicate in a large list should be caught
    const items = Array.from({ length: 100 }, (_, i) => `Species ${i}`);
    items[99] = 'Species 0'; // Duplicate of first item

    expect(() => {
      render(StringListKey, { items });
    }).toThrow(/each_key_duplicate|duplicate key/i);
  });
});
