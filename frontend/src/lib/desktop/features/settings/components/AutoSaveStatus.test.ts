import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import type { StagedSaveResult } from '$lib/stores/settings';
import { deferred } from '../../../../../test/async-helpers';

const store = vi.hoisted(() => ({
  stageSectionEdit: vi.fn(),
  saveStagedSection: vi.fn(),
  onStagedEditsDropped: vi.fn(() => () => undefined),
}));

vi.mock('$lib/stores/settings', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/stores/settings')>();
  return { ...actual, settingsActions: { ...actual.settingsActions, ...store } };
});

import { commitSetting, resetAutoSaveForTest } from '$lib/stores/settingsAutoSave.svelte';
import AutoSaveStatus from './AutoSaveStatus.svelte';

const FIELD = 'audio:a:models';
const OK: StagedSaveResult = { saved: [], dropped: [], failed: [], remaining: false };

function commit() {
  commitSetting('audio', { sources: [] }, { field: FIELD });
}

describe('AutoSaveStatus', () => {
  beforeEach(() => {
    resetAutoSaveForTest();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset().mockResolvedValue({ ...OK, saved: [['sources']] });
  });

  afterEach(() => {
    resetAutoSaveForTest();
  });

  it('keeps an empty live region while the field is idle', () => {
    const { container } = render(AutoSaveStatus, { props: { field: FIELD } });

    expect(screen.getByRole('status')).toHaveTextContent('');
    expect(screen.queryByRole('alert')).toBeNull();
    expect(container.textContent.trim()).toBe('');
  });

  it('announces Saving through the same region that later announces Saved', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    render(AutoSaveStatus, { props: { field: FIELD } });
    const region = screen.getByRole('status');

    commit();
    await vi.waitFor(() => expect(region).toHaveTextContent('settings.actions.saving'));

    pending.resolve({ ...OK, saved: [['sources']] });
    await vi.waitFor(() => expect(region).toHaveTextContent('settings.autoSave.saved'));
    expect(screen.getByRole('status')).toBe(region);
  });

  it('shows the reason with the id, announces it, and keeps focus nearby after dismissal', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    const { container } = render(AutoSaveStatus, { props: { field: FIELD, id: 'status-1' } });

    commit();
    const dismiss = await screen.findByRole('button', { name: 'settings.autoSave.dismiss' });
    expect(screen.getByRole('status')).toHaveTextContent('settings.autoSave.failed');
    const message = document.getElementById('status-1');
    expect(message).toHaveTextContent('settings.autoSave.failed');

    dismiss.focus();
    await fireEvent.click(dismiss);

    expect(document.getElementById('status-1')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('');
    expect(document.activeElement).toBe(container.firstElementChild);
  });
});
