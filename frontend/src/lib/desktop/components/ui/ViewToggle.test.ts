import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { renderTyped } from '../../../../test/render-helpers';
import ViewToggle from './ViewToggle.svelte';

describe('ViewToggle', () => {
  afterEach(cleanup);

  it('makes only the checked view a Tab stop', () => {
    renderTyped(ViewToggle, { props: { view: 'cards', onViewChange: vi.fn() } });

    expect(screen.getByRole('radio', { name: 'detections.viewToggle.cards' })).toHaveAttribute(
      'tabindex',
      '0'
    );
    expect(screen.getByRole('radio', { name: 'detections.viewToggle.table' })).toHaveAttribute(
      'tabindex',
      '-1'
    );
  });

  it('selects the other view and moves focus to it on an arrow key', async () => {
    const onViewChange = vi.fn();
    renderTyped(ViewToggle, { props: { view: 'table', onViewChange } });
    const table = screen.getByRole('radio', { name: 'detections.viewToggle.table' });
    const cards = screen.getByRole('radio', { name: 'detections.viewToggle.cards' });

    table.focus();
    await fireEvent.keyDown(table, { key: 'ArrowRight' });

    expect(onViewChange).toHaveBeenCalledWith('cards');
    await waitFor(() => expect(document.activeElement).toBe(cards));
  });
});
