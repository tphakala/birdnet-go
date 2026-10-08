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

  it('names the dialog with its title', async () => {
    await renderOpen();

    const dialog = screen.getByRole('dialog');
    const heading = screen.getByRole('heading', { level: 3 });
    expect(dialog).toHaveAccessibleName(heading.textContent.trim());
    expect(dialog.getAttribute('aria-labelledby')).toBe(heading.id);
  });

  it('describes the dialog with its message only', async () => {
    await renderOpen();

    const dialog = screen.getByRole('dialog');
    const describedBy = dialog.getAttribute('aria-describedby') ?? '';
    const message = document.getElementById(describedBy);
    expect(message?.tagName).toBe('P');
    expect(dialog).toHaveAccessibleDescription(message?.textContent.trim() ?? '');
    expect(screen.getByRole('checkbox').closest(`#${describedBy}`)).toBeNull();
  });

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
