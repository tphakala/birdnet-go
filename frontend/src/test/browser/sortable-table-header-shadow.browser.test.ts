/**
 * Browser Mode Test: a caller's shadow utility wins over the table's header shadow
 *
 * The table gives every header cell an inset bottom shadow from component CSS. That rule
 * must sit in the components layer, below Tailwind's utilities, so a shadow or ring utility
 * a caller passes through column.className replaces it. jsdom does not evaluate layers.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';

import ShadowedSortableTable from './wrappers/ShadowedSortableTable.svelte';

// The theme stylesheet is not loaded here; the shadow paints in the border token
const BORDER_COLOR = 'rgb(1, 2, 3)';
const CALLER_SHADOW_COLOR = 'rgb(255, 0, 0)';

function shadowOf(label: string): string {
  const cell = [...document.querySelectorAll('th')].find(th => th.textContent.includes(label));
  if (!cell) throw new Error(`header cell ${label} not rendered`);
  return getComputedStyle(cell).boxShadow;
}

describe('SortableDataTable header shadow in a real browser', () => {
  afterEach(() => {
    document.documentElement.style.removeProperty('--border-100');
  });

  it('keeps its own inset shadow on a cell without a caller shadow', async () => {
    document.documentElement.style.setProperty('--border-100', BORDER_COLOR);
    await render(ShadowedSortableTable, {});

    expect(shadowOf('Name')).toContain(BORDER_COLOR);
    expect(shadowOf('Name')).toContain('inset');
  });

  it('lets a caller shadow utility replace the table shadow', async () => {
    document.documentElement.style.setProperty('--border-100', BORDER_COLOR);
    await render(ShadowedSortableTable, {});

    expect(shadowOf('Score')).toContain(CALLER_SHADOW_COLOR);
    expect(shadowOf('Score')).not.toContain(BORDER_COLOR);
  });
});
