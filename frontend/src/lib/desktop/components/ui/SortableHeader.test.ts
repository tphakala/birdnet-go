import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'svelte';
import { renderTyped } from '../../../../test/render-helpers';
import SortableHeader from './SortableHeader.svelte';

type HeaderProps = ComponentProps<typeof SortableHeader>;

function renderHeader(overrides: Partial<HeaderProps> = {}) {
  const props: HeaderProps = {
    label: 'Score',
    field: 'score',
    activeField: 'name',
    direction: 'asc',
    onSort: vi.fn(),
    ...overrides,
  };
  renderTyped(SortableHeader, { props });
  return { props };
}

function header(): HTMLTableCellElement {
  const cell = document.querySelector('th');
  if (!cell) throw new Error('no header cell rendered');
  return cell;
}

/** Returns the sort indicator and the label span inside the header button. */
function parts(): { indicator: Element; label: Element } {
  const button = screen.getByRole('button');
  const indicator = button.querySelector('[aria-hidden="true"]');
  const label = button.querySelector('[data-sort-label]');
  if (!indicator || !label) throw new Error('indicator or label missing');
  return { indicator, label };
}

describe('SortableHeader', () => {
  afterEach(() => {
    cleanup();
  });

  it.each([
    { activeField: 'score', direction: 'asc' as const, expected: 'ascending' },
    { activeField: 'score', direction: 'desc' as const, expected: 'descending' },
    { activeField: 'name', direction: 'asc' as const, expected: 'none' },
  ])(
    'sets aria-sort to $expected when active=$activeField and direction=$direction',
    ({ activeField, direction, expected }) => {
      renderHeader({ activeField, direction });
      expect(header()).toHaveAttribute('aria-sort', expected);
    }
  );

  it('calls onSort with its field when the button is clicked', async () => {
    const user = userEvent.setup();
    const { props } = renderHeader();
    await user.click(screen.getByRole('button'));
    expect(props.onSort).toHaveBeenCalledWith('score');
  });

  it('puts the indicator before the label when right-aligned', () => {
    renderHeader({ align: 'right' });
    const { indicator, label } = parts();
    expect(indicator.compareDocumentPosition(label)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(header()).toHaveClass('text-right');
  });

  it('puts the indicator after the label by default', () => {
    renderHeader();
    const { indicator, label } = parts();
    expect(label.compareDocumentPosition(indicator)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });
});
