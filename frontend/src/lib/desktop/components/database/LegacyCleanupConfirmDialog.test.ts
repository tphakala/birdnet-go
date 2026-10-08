import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import LegacyCleanupConfirmDialog from './LegacyCleanupConfirmDialog.svelte';

describe('LegacyCleanupConfirmDialog', () => {
  it('describes the dialog with its message', () => {
    render(LegacyCleanupConfirmDialog, {
      props: {
        open: true,
        sizeBytes: 1024,
        isLoading: false,
        onConfirm: vi.fn(),
        onCancel: vi.fn(),
      },
    });

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAccessibleName(/\S/);
    const describedBy = dialog.getAttribute('aria-describedby') ?? '';
    const message = document.getElementById(describedBy);
    expect(message?.tagName).toBe('P');
    expect(dialog).toHaveAccessibleDescription(message?.textContent.trim() ?? '');
    expect(screen.getByRole('checkbox').closest(`#${describedBy}`)).toBeNull();
  });

  it('Tab from Cancel stays in the dialog while Delete is disabled', async () => {
    const user = userEvent.setup();
    render(LegacyCleanupConfirmDialog, {
      props: {
        open: true,
        sizeBytes: 1024,
        isLoading: false,
        onConfirm: vi.fn(),
        onCancel: vi.fn(),
      },
    });
    await waitFor(() => {
      expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
    });
    const [cancel, remove] = screen.getAllByRole('button');
    expect(remove).toBeDisabled();
    cancel.focus();

    await user.tab();

    expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(remove);
  });
});
