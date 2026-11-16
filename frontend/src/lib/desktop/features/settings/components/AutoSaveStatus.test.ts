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

  it('renders nothing while the field is idle', () => {
    const { container } = render(AutoSaveStatus, { props: { field: FIELD } });

    expect(screen.queryByRole('status')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(container.textContent.trim()).toBe('');
  });

  it('announces Saving while the request runs and Saved after it', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    render(AutoSaveStatus, { props: { field: FIELD, id: 'status-1' } });

    commit();
    const saving = await screen.findByRole('status');
    expect(saving).toHaveTextContent('settings.actions.saving');
    expect(saving).toHaveAttribute('id', 'status-1');

    pending.resolve({ ...OK, saved: [['sources']] });
    const done = await screen.findByText('settings.autoSave.saved');
    expect(done.closest('[role="status"]')).not.toBeNull();
  });

  it('shows the reason in an alert with a dismiss button that clears it', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    render(AutoSaveStatus, { props: { field: FIELD } });

    commit();
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent('settings.autoSave.failed');

    await fireEvent.click(screen.getByRole('button', { name: 'settings.autoSave.dismiss' }));
    expect(screen.queryByRole('alert')).toBeNull();
  });
});
