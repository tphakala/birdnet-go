/**
 * SourceCard: the shell of one source. The header button opens and closes the
 * body, and delete asks for confirmation in an overlay that takes focus, goes
 * back on Cancel or Escape and deletes only when confirmed and not disabled.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { Mic } from '@lucide/svelte';
import SourceCard from './SourceCard.svelte';

const DELETE = /delete/i;
const CANCEL = /cancel/i;

/** The Delete button inside the confirmation overlay, not the one in the header. */
function confirmButton(dialog: HTMLElement): HTMLElement {
  const button = screen.getAllByRole('button', { name: DELETE }).find(b => dialog.contains(b));
  if (!button) throw new Error('the overlay has no Delete button');
  return button;
}

function renderCard(extra: Partial<{ expanded: boolean; disabled: boolean }> = {}) {
  const onToggleExpanded = vi.fn();
  const onDelete = vi.fn();
  const view = render(SourceCard, {
    props: {
      name: 'Garden',
      subtitle: 'hw:1',
      icon: Mic,
      expanded: false,
      onToggleExpanded,
      deleteConfirm: 'Delete source',
      onDelete,
      children: createRawSnippet(() => ({ render: () => '<p>body</p>' })),
      ...extra,
    },
  });
  return { view, onToggleExpanded, onDelete };
}

describe('SourceCard', () => {
  afterEach(() => {
    cleanup();
  });

  it('names the disclosure button after the source and reports its expanded state', async () => {
    const { onToggleExpanded } = renderCard({ expanded: true });

    const toggle = screen.getByRole('button', { name: /Garden/ });
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByText('body')).toBeInTheDocument();

    await fireEvent.click(toggle);
    expect(onToggleExpanded).toHaveBeenCalledOnce();
  });

  it('keeps the body out of the page while collapsed', () => {
    renderCard({ expanded: false });

    expect(screen.queryByText('body')).toBeNull();
  });

  it('asks for confirmation, moves focus to Cancel, and deletes only after Delete in the overlay', async () => {
    const { onDelete } = renderCard();

    await fireEvent.click(screen.getByRole('button', { name: DELETE }));
    const dialog = await screen.findByRole('alertdialog');
    expect(dialog).toHaveTextContent('Delete source');
    expect(dialog).toHaveTextContent('Garden');
    await vi.waitFor(() => expect(screen.getByRole('button', { name: CANCEL })).toHaveFocus());
    expect(onDelete).not.toHaveBeenCalled();

    await fireEvent.click(confirmButton(dialog));
    expect(onDelete).toHaveBeenCalledOnce();
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });

  it('closes the overlay on Cancel and on Escape and returns focus to the delete button', async () => {
    const { onDelete } = renderCard();
    const trigger = screen.getByRole('button', { name: DELETE });

    await fireEvent.click(trigger);
    await fireEvent.click(await screen.findByRole('button', { name: CANCEL }));
    expect(screen.queryByRole('alertdialog')).toBeNull();
    await vi.waitFor(() => expect(trigger).toHaveFocus());

    await fireEvent.click(trigger);
    await fireEvent.keyDown(await screen.findByRole('alertdialog'), { key: 'Escape' });
    expect(screen.queryByRole('alertdialog')).toBeNull();
    await vi.waitFor(() => expect(trigger).toHaveFocus());
    expect(onDelete).not.toHaveBeenCalled();
  });

  it('opens no overlay while disabled, and deletes nothing if it became disabled while open', async () => {
    const { view, onDelete } = renderCard({ disabled: true });
    await fireEvent.click(screen.getByRole('button', { name: DELETE }));
    expect(screen.queryByRole('alertdialog')).toBeNull();

    await view.rerender({ disabled: false });
    await fireEvent.click(screen.getByRole('button', { name: DELETE }));
    const dialog = await screen.findByRole('alertdialog');
    await view.rerender({ disabled: true });
    await fireEvent.click(confirmButton(dialog));

    expect(onDelete).not.toHaveBeenCalled();
    expect(screen.queryByRole('alertdialog')).toBeNull();
  });
});
