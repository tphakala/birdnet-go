import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import MigrationConfirmDialog from './MigrationConfirmDialog.svelte';

describe('MigrationConfirmDialog', () => {
  let user: ReturnType<typeof userEvent.setup>;

  beforeEach(() => {
    user = userEvent.setup();
  });

  // The footer buttons, in DOM order: Cancel, then Start
  const footerButtons = () => {
    const [cancel, start] = screen.getAllByRole('button');
    return { cancel, start };
  };

  async function renderOpen() {
    render(MigrationConfirmDialog, {
      props: { open: true, onConfirm: vi.fn(), onCancel: vi.fn() },
    });
    await waitFor(() => {
      expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
    });
  }

  it('Tab from Cancel stays in the dialog while Start is disabled', async () => {
    await renderOpen();
    const { cancel, start } = footerButtons();
    expect(start).toBeDisabled();
    cancel.focus();

    await user.tab();

    expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(start);
  });

  it('Tab reaches Start once the checkbox is checked', async () => {
    await renderOpen();
    const { cancel, start } = footerButtons();
    await user.click(screen.getByRole('checkbox'));
    expect(start).toBeEnabled();
    cancel.focus();

    await user.tab();

    expect(start).toHaveFocus();
  });
});
