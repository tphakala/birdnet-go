/* global $state, $effect */
/**
 * ApplyDraft: the draft of a group saved with a scoped Apply follows the saved
 * values until edited, commits once on Apply, returns to the saved values after a
 * save and keeps the edit after a failure.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
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

const FIELD = 'audio:sources:r:capture';
const OK: StagedSaveResult = { saved: [], dropped: [], failed: [], remaining: false };

interface Capture {
  device: string;
  sampleRate: number;
}

describe('ApplyDraft', () => {
  let saved = $state<Capture>({ device: 'hw:1', sampleRate: 48000 });
  let draft: ApplyDraft<Capture>;
  let stop: () => void;

  function commit(value: Capture) {
    saved = value; // staging writes the form at once, like the store does
    commitSetting('audio', { sources: [] }, { field: FIELD });
  }

  beforeEach(() => {
    resetAutoSaveForTest();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset();
    saved = { device: 'hw:1', sampleRate: 48000 };
    stop = $effect.root(() => {
      draft = new ApplyDraft(() => saved, FIELD);
    });
    flushSync();
  });

  afterEach(() => {
    stop();
    resetAutoSaveForTest();
  });

  it('follows the saved values until edited, then keeps the edit', () => {
    saved = { device: 'hw:2', sampleRate: 48000 };
    flushSync();
    expect(draft.draft.device).toBe('hw:2');
    expect(draft.visible).toBe(false);

    draft.set({ device: 'hw:3' });
    saved = { device: 'hw:4', sampleRate: 48000 };
    flushSync();
    expect(draft.draft.device).toBe('hw:3');
    expect(draft.visible).toBe(true);

    draft.cancel();
    expect(draft.draft.device).toBe('hw:4');
    expect(draft.visible).toBe(false);
  });

  it('stays visible and busy while the apply is saved, then hands back to the saved values', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    draft.set({ device: 'hw:2' });

    draft.apply(commit);
    flushSync();
    expect(draft.applying).toBe(true);
    // The form already holds the draft, yet the Apply group stays until the result.
    expect(draft.visible).toBe(true);
    draft.apply(commit);
    expect(store.stageSectionEdit).toHaveBeenCalledOnce();

    pending.resolve({ ...OK, saved: [['sources']] });
    await vi.waitFor(() => expect(draft.applying).toBe(false));
    flushSync();
    expect(draft.edited).toBe(false);
    expect(draft.visible).toBe(false);
  });

  it('keeps the draft after a failed apply so it can be applied again or cancelled', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    draft.set({ device: 'hw:2' });

    draft.apply(value => {
      commit(value);
    });
    await vi.waitFor(() => expect(draft.failed).toBe(true));
    // The store took the form back to the stored value; the draft stays.
    saved = { device: 'hw:1', sampleRate: 48000 };
    flushSync();
    expect(draft.draft.device).toBe('hw:2');
    expect(draft.visible).toBe(true);
  });

  it('follows the saved values again once the draft is set back to them', () => {
    draft.set({ device: 'hw:3' });
    expect(draft.edited).toBe(true);
    draft.set({ device: 'hw:1' });
    expect(draft.edited).toBe(false);

    saved = { device: 'hw:5', sampleRate: 48000 };
    flushSync();
    expect(draft.draft.device).toBe('hw:5');
    expect(draft.visible).toBe(false);
  });

  it('drops the failure of an apply when the draft is cancelled', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    draft.set({ device: 'hw:2' });
    draft.apply(commit);
    await vi.waitFor(() => expect(draft.failed).toBe(true));

    draft.cancel();
    expect(draft.failed).toBe(false);
  });

  it('keeps an edit made while the apply runs, waiting for its own Apply', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    draft.set({ device: 'hw:2' });
    draft.apply(commit);
    flushSync();

    draft.set({ sampleRate: 96000 });
    pending.resolve({ ...OK, saved: [['sources']] });
    await vi.waitFor(() => expect(draft.applying).toBe(false));
    flushSync();

    expect(draft.draft).toEqual({ device: 'hw:2', sampleRate: 96000 });
    expect(draft.visible).toBe(true);
  });

  it('keeps a draft set back to the sent values during an apply when that apply fails', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    draft.set({ device: 'hw:2' });
    draft.apply(commit);
    flushSync();

    // While the apply runs the form holds the sent draft, so this equals the saved values.
    draft.set({ device: 'hw:2' });
    pending.resolve({ ...OK, failed: [['sources']], error: new Error('boom') });
    await vi.waitFor(() => expect(draft.failed).toBe(true));
    saved = { device: 'hw:1', sampleRate: 48000 }; // the store takes the form back
    flushSync();

    expect(draft.draft.device).toBe('hw:2');
    expect(draft.visible).toBe(true);
  });
});
