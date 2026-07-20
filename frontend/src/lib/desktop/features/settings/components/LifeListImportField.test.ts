import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import LifeListImportField from './LifeListImportField.svelte';

// This test suite matches literal i18n key strings rather than translated
// English text, following this codebase's convention (t() returns the raw
// key in the test environment; see SpeciesConfigTable.test.ts). Exception:
// ConfirmModal's own default confirm/cancel labels resolve to real "Confirm"/
// "Cancel" text because those keys happen to be in the curated test-mock
// dictionary (see ConfirmModal.test.ts) — we deliberately don't override
// confirmLabel with our own lifeList-specific key, both to avoid duplicating
// the trigger button's text (which caused a real query-ambiguity bug here)
// and to match the established convention elsewhere (e.g. RestartCard.svelte).
const KEY_EMPTY_MESSAGE = 'settings.species.lifeList.emptyMessage';
const KEY_SPECIES_COUNT = 'settings.species.lifeList.speciesCount';
const KEY_UPLOAD_CSV = 'settings.species.lifeList.uploadCsv';
const KEY_DISMISS = 'common.ui.dismiss';
const KEY_SHOW_REJECTED_ROWS = 'settings.species.lifeList.showRejectedRows';
const KEY_IMPORT_SUMMARY = 'settings.species.lifeList.importSummary';
const KEY_CLEAR_LIST = 'settings.species.lifeList.clearList';

function renderField(overrides: Record<string, unknown> = {}) {
  const onImport = vi.fn();
  const onClear = vi.fn();
  render(LifeListImportField, {
    props: {
      speciesCount: 0,
      onImport,
      onClear,
      ...overrides,
    },
  });
  return { onImport, onClear };
}

describe('LifeListImportField', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows the empty message when the count is zero', () => {
    renderField();
    expect(screen.getByText(KEY_EMPTY_MESSAGE)).toBeInTheDocument();
  });

  it('shows the current count badge', () => {
    renderField({ speciesCount: 426 });
    expect(screen.getByText(KEY_SPECIES_COUNT)).toBeInTheDocument();
  });

  it('does not show the empty message when the count is nonzero', () => {
    renderField({ speciesCount: 5 });
    expect(screen.queryByText(KEY_EMPTY_MESSAGE)).not.toBeInTheDocument();
  });

  it('disables the upload button when disabled is true', () => {
    renderField({ disabled: true });
    expect(screen.getByRole('button', { name: KEY_UPLOAD_CSV })).toBeDisabled();
  });

  it('parses an uploaded CSV and calls onImport with the accepted entries (replacing the list)', async () => {
    const { onImport } = renderField({ speciesCount: 10 });
    const user = userEvent.setup();

    const csv = 'Common Name,Scientific Name\nCommon Blackbird,Turdus merula\n';
    const file = new File([csv], 'life-list.csv', { type: 'text/csv' });

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(fileInput, file);

    // handleFileSelect is async (awaits readFileAsText); wait for its
    // completion signal before asserting on onImport, or this races.
    expect(await screen.findByText(KEY_IMPORT_SUMMARY)).toBeInTheDocument();
    expect(onImport).toHaveBeenCalledWith(['Turdus merula_Common Blackbird']);
  });

  it('shows a rejected-rows toggle and reveals rows on click, still calling onImport with the accepted subset', async () => {
    const { onImport } = renderField();
    const user = userEvent.setup();

    const csv = [
      'Common Name,Scientific Name',
      'Common Blackbird,Turdus merula',
      'Garbage,not a species',
    ].join('\n');
    const file = new File([csv], 'life-list.csv', { type: 'text/csv' });

    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(fileInput, file);

    expect(await screen.findByText(KEY_IMPORT_SUMMARY)).toBeInTheDocument();
    expect(onImport).toHaveBeenCalledWith(['Turdus merula_Common Blackbird']);

    await user.click(screen.getByText(KEY_SHOW_REJECTED_ROWS));
    expect(screen.getByText(/not a species/i)).toBeInTheDocument();
  });

  it('dismisses the import summary when Dismiss is clicked', async () => {
    renderField();
    const user = userEvent.setup();

    const csv = 'Common Name,Scientific Name\nCommon Blackbird,Turdus merula\n';
    const file = new File([csv], 'life-list.csv', { type: 'text/csv' });
    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(fileInput, file);

    const dismissButton = await screen.findByText(KEY_DISMISS);
    await user.click(dismissButton);

    expect(screen.queryByText(KEY_DISMISS)).not.toBeInTheDocument();
  });

  it('does not show a Clear List button when the count is zero', () => {
    renderField({ speciesCount: 0 });
    expect(screen.queryByText(KEY_CLEAR_LIST)).not.toBeInTheDocument();
  });

  it('shows a Clear List button when the count is nonzero', () => {
    renderField({ speciesCount: 426 });
    expect(screen.getByText(KEY_CLEAR_LIST)).toBeInTheDocument();
  });

  it('does not call onClear until the confirmation dialog is confirmed', async () => {
    const { onClear } = renderField({ speciesCount: 426 });
    const user = userEvent.setup();

    await user.click(screen.getByText(KEY_CLEAR_LIST));
    expect(onClear).not.toHaveBeenCalled();

    const dialog = screen.getByRole('dialog');
    await user.click(within(dialog).getByText('Cancel'));
    expect(onClear).not.toHaveBeenCalled();
  });

  it('calls onClear when the clear action is confirmed', async () => {
    const { onClear } = renderField({ speciesCount: 426 });
    const user = userEvent.setup();

    await user.click(screen.getByText(KEY_CLEAR_LIST));
    const dialog = screen.getByRole('dialog');
    await user.click(within(dialog).getByText('Confirm'));

    expect(onClear).toHaveBeenCalledTimes(1);
  });

  it('dismisses any visible import summary when the clear action is confirmed', async () => {
    renderField({ speciesCount: 426 });
    const user = userEvent.setup();

    const csv = 'Common Name,Scientific Name\nCommon Blackbird,Turdus merula\n';
    const file = new File([csv], 'life-list.csv', { type: 'text/csv' });
    const fileInput = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(fileInput, file);
    expect(await screen.findByText(KEY_IMPORT_SUMMARY)).toBeInTheDocument();

    await user.click(screen.getByText(KEY_CLEAR_LIST));
    const dialog = screen.getByRole('dialog');
    await user.click(within(dialog).getByText('Confirm'));

    expect(screen.queryByText(KEY_IMPORT_SUMMARY)).not.toBeInTheDocument();
  });
});
