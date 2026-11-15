/**
 * Browser Mode Test: the sort button covers its whole header cell
 *
 * jsdom has no layout, so only a real browser shows whether a click in the cell
 * padding reaches the button and whether the keyboard focus ring is drawn around
 * the cell instead of around the label.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, vi, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { page, userEvent } from 'vitest/browser';

import PaddedSortableHeader from './wrappers/PaddedSortableHeader.svelte';

// A click this far from the corner of the cell lands in its padding, outside the label
const CORNER_OFFSET_PX = 3;
const RING_WIDTH = '2px';
// Negative: the ring is drawn inside the cell, where scroll wrappers cannot clip it
const RING_OFFSET = '-2px';
// The theme stylesheet is not loaded here; the ring paints in the primary token
const PRIMARY_COLOR = 'rgb(10, 20, 30)';

function headerCell(): HTMLTableCellElement {
  const cell = document.querySelector('th');
  if (!cell) throw new Error('header cell not rendered');
  return cell;
}

describe('SortableHeader hit area in a real browser', () => {
  afterEach(() => {
    document.documentElement.style.removeProperty('--color-primary');
  });

  it('sorts when the click lands in the cell padding', async () => {
    const onSort = vi.fn();
    await render(PaddedSortableHeader, { props: { onSort } });

    await page.elementLocator(headerCell()).click({
      position: { x: CORNER_OFFSET_PX, y: CORNER_OFFSET_PX },
    });

    expect(onSort).toHaveBeenCalledWith('score');
  });

  it.each([
    { extraClass: 'sticky', position: 'sticky' },
    { extraClass: 'md:sticky', position: 'sticky' },
    { extraClass: 'absolute', position: 'absolute' },
    { extraClass: '', position: 'relative' },
  ])(
    'positions the header cell $position for the class "$extraClass"',
    async ({ extraClass, position }) => {
      await render(PaddedSortableHeader, { props: { onSort: vi.fn(), extraClass } });

      expect(getComputedStyle(headerCell()).position).toBe(position);
    }
  );

  it('draws the keyboard focus ring on a layer that spans the cell, not on the button', async () => {
    document.documentElement.style.setProperty('--color-primary', PRIMARY_COLOR);
    await render(PaddedSortableHeader, { props: { onSort: vi.fn() } });

    await userEvent.tab();

    const button = document.querySelector('button');
    if (!button) throw new Error('sort button not rendered');
    expect(button).toHaveFocus();
    expect(getComputedStyle(button).outlineStyle).toBe('none');
    const ring = getComputedStyle(button, '::after');
    expect(ring.outlineStyle).toBe('solid');
    expect(ring.outlineWidth).toBe(RING_WIDTH);
    expect(ring.outlineColor).toBe(PRIMARY_COLOR);
    expect(ring.outlineOffset).toBe(RING_OFFSET);
    const cell = headerCell().getBoundingClientRect();
    expect(Number.parseFloat(ring.width)).toBeCloseTo(cell.width, 0);
    expect(Number.parseFloat(ring.height)).toBeCloseTo(cell.height, 0);
  });
});
