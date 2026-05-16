import { describe, it, expect } from 'vitest';
import { flushSync } from 'svelte';
import { useSelectionMode } from './useSelectionMode.svelte';

describe('useSelectionMode', () => {
  function createSelection(totalMatchingCount = 100) {
    return useSelectionMode(totalMatchingCount);
  }

  it('starts inactive with empty selection', () => {
    const s = createSelection();
    expect(s.selectionActive).toBe(false);
    expect(s.selectedCount).toBe(0);
  });

  it('activates and deactivates selection mode', () => {
    const s = createSelection();
    s.activate();
    flushSync();
    expect(s.selectionActive).toBe(true);
    s.deactivate();
    flushSync();
    expect(s.selectionActive).toBe(false);
    expect(s.selectedCount).toBe(0);
  });

  it('toggles individual IDs', () => {
    const s = createSelection();
    s.activate();
    s.toggle('1');
    flushSync();
    expect(s.isSelected('1')).toBe(true);
    expect(s.selectedCount).toBe(1);
    s.toggle('1');
    flushSync();
    expect(s.isSelected('1')).toBe(false);
    expect(s.selectedCount).toBe(0);
  });

  it('toggles all on page', () => {
    const s = createSelection();
    s.activate();
    const pageIds = ['1', '2', '3'];
    s.toggleAllOnPage(pageIds);
    flushSync();
    expect(s.allOnPageSelected(pageIds)).toBe(true);
    expect(s.selectedCount).toBe(3);
    s.toggleAllOnPage(pageIds);
    flushSync();
    expect(s.allOnPageSelected(pageIds)).toBe(false);
    expect(s.selectedCount).toBe(0);
  });

  it('handles shift-click range selection', () => {
    const s = createSelection();
    s.activate();
    const pageIds = ['a', 'b', 'c', 'd', 'e'];
    s.toggleWithShift('b', pageIds, false);
    flushSync();
    expect(s.selectedCount).toBe(1);
    s.toggleWithShift('d', pageIds, true);
    flushSync();
    expect(s.isSelected('b')).toBe(true);
    expect(s.isSelected('c')).toBe(true);
    expect(s.isSelected('d')).toBe(true);
    expect(s.selectedCount).toBe(3);
  });

  it('selects all matching', () => {
    const s = createSelection(150);
    s.activate();
    s.selectAllMatching();
    flushSync();
    expect(s.selectedCount).toBe(150);
    expect(s.isSelected('anything')).toBe(true);
  });

  it('clear resets everything', () => {
    const s = createSelection(150);
    s.activate();
    s.toggle('1');
    s.selectAllMatching();
    s.clear();
    flushSync();
    expect(s.selectedCount).toBe(0);
    expect(s.selectionActive).toBe(true);
  });

  it('deactivate clears selection and exits mode', () => {
    const s = createSelection();
    s.activate();
    s.toggle('1');
    s.deactivate();
    flushSync();
    expect(s.selectionActive).toBe(false);
    expect(s.selectedCount).toBe(0);
  });

  it('someOnPageSelected is true when partially selected', () => {
    const s = createSelection();
    s.activate();
    const pageIds = ['1', '2', '3'];
    s.toggle('1');
    flushSync();
    expect(s.someOnPageSelected(pageIds)).toBe(true);
    expect(s.allOnPageSelected(pageIds)).toBe(false);
  });

  it('getSelectedIds returns current set as array', () => {
    const s = createSelection();
    s.activate();
    s.toggle('a');
    s.toggle('b');
    flushSync();
    const ids = s.getSelectedIds();
    expect(ids).toHaveLength(2);
    expect(ids).toContain('a');
    expect(ids).toContain('b');
  });
});
