/* global $state, $effect */
/**
 * ScopedApplyGroup: shows the Apply row only while the draft differs or an apply
 * runs, names the Apply button for its effect, keeps the draft and says so after
 * a failed apply, and ignores clicks while an apply is saved.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { flushSync } from 'svelte';
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
import { ApplyDraft } from './applyDraft.svelte';
import ScopedApplyGroup from './ScopedApplyGroup.svelte';

const FIELD = 'audio:sources:r:capture';
const STATUS_ID = 'capture-status';
const OK: StagedSaveResult = { saved: [], dropped: [], failed: [], remaining: false };
const HINT = 'Capture restarts when you apply.';
const FAILED_HINT = 'Capture was not changed.';
const APPLY = 'Apply and restart capture';
const APPLYING = 'Applying...';

interface Capture {
  device: string;
}

describe('ScopedApplyGroup', () => {
  let saved = $state<Capture>({ device: 'hw:1' });
  let draft: ApplyDraft<Capture>;
  let stop: () => void;
  const onApply = vi.fn();

  function commit(value: Capture) {
    saved = value; // staging writes the form at once, like the store does
    onApply(value);
    commitSetting('audio', { sources: [] }, { field: FIELD });
  }

  function renderGroup(disabled = false) {
    return render(ScopedApplyGroup<Capture>, {
      props: {
        draft,
        onApply: commit,
        hint: HINT,
        failedHint: FAILED_HINT,
        applyLabel: APPLY,
        applyingLabel: APPLYING,
        statusId: STATUS_ID,
        disabled,
      },
    });
  }

  beforeEach(() => {
    resetAutoSaveForTest();
    onApply.mockReset();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset();
    saved = { device: 'hw:1' };
    stop = $effect.root(() => {
      draft = new ApplyDraft(() => saved, FIELD);
    });
    flushSync();
  });

  afterEach(() => {
    cleanup();
    stop();
    resetAutoSaveForTest();
  });

  it('shows no Apply row while the draft equals the saved values', () => {
    renderGroup();

    expect(screen.queryByText(HINT)).toBeNull();
    expect(screen.queryByRole('button', { name: APPLY })).toBeNull();
  });

  it('shows the hint and the named Apply button for a differing draft, and Cancel restores the saved values', async () => {
    renderGroup();
    draft.set({ device: 'hw:2' });

    expect(await screen.findByText(HINT)).toBeInTheDocument();
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(draft.draft.device).toBe('hw:1');
    expect(screen.queryByText(HINT)).toBeNull();
  });

  it('applies the draft once and ignores a second click while the apply is saved', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    renderGroup();
    draft.set({ device: 'hw:2' });

    await fireEvent.click(await screen.findByRole('button', { name: APPLY }));
    const busy = await screen.findByRole('button', { name: APPLYING });
    expect(busy).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(busy);
    expect(onApply).toHaveBeenCalledExactlyOnceWith({ device: 'hw:2' });

    pending.resolve({ ...OK, saved: [['sources']] });
    await vi.waitFor(() => expect(screen.queryByText(APPLYING)).toBeNull());
  });

  it('keeps the draft after a failed apply, says so, and points the button at the failure', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    renderGroup();
    draft.set({ device: 'hw:2' });

    await fireEvent.click(await screen.findByRole('button', { name: APPLY }));
    // The store takes the form back to the stored value; the draft stays.
    await vi.waitFor(() => expect(draft.failed).toBe(true));
    saved = { device: 'hw:1' };

    expect(await screen.findByText(FAILED_HINT)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: APPLY })).toHaveAttribute(
      'aria-describedby',
      STATUS_ID
    );
    expect(draft.draft.device).toBe('hw:2');
  });

  it('does not apply while disabled', async () => {
    renderGroup(true);
    draft.set({ device: 'hw:2' });

    const apply = await screen.findByRole('button', { name: APPLY });
    expect(apply).toHaveAttribute('aria-disabled', 'true');
    await fireEvent.click(apply);
    expect(onApply).not.toHaveBeenCalled();
  });
});
