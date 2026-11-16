import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { AudioSourceConfig, StagedSaveResult, DroppedStagedEdit } from './settings';
import { deferred } from '../../test/async-helpers';

type DropListener = (edits: DroppedStagedEdit[]) => void;

const store = vi.hoisted(() => ({
  stageSectionEdit: vi.fn(),
  saveStagedSection: vi.fn(),
  onStagedEditsDropped: vi.fn(),
}));

vi.mock('./settings', async importOriginal => {
  const actual = await importOriginal<typeof import('./settings')>();
  return { ...actual, settingsActions: { ...actual.settingsActions, ...store } };
});

import {
  AUTOSAVE_DEBOUNCE_MS,
  AUTOSAVE_SAVED_VISIBLE_MS,
  autoSaveBusy,
  autoSaveFailures,
  autoSaveStatus,
  commitSetting,
  dismissAutoSaveError,
  flushAutoSaves,
  resetAutoSaveForTest,
} from './settingsAutoSave.svelte';

const OK: StagedSaveResult = { saved: [], dropped: [], failed: [], remaining: false };

/** A complete source, so a commit carries the whole typed array. */
const source = (extra: Partial<AudioSourceConfig> = {}): AudioSourceConfig => ({
  name: 'a',
  device: 'hw:0',
  gain: 0,
  models: [],
  ...extra,
});

/** A successful save of the given paths of the audio section. */
const saved = (...paths: string[][]): StagedSaveResult => ({ ...OK, saved: paths });

describe('settings auto save engine', () => {
  let dropListener: DropListener | undefined;

  beforeEach(() => {
    vi.useFakeTimers();
    resetAutoSaveForTest();
    dropListener = undefined;
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset().mockResolvedValue(OK);
    store.onStagedEditsDropped.mockReset().mockImplementation((listener: DropListener) => {
      dropListener = listener;
      return () => {
        dropListener = undefined;
      };
    });
  });

  afterEach(() => {
    resetAutoSaveForTest();
    vi.useRealTimers();
  });

  it('makes one save for three slider commits inside the debounce window', async () => {
    store.saveStagedSection.mockResolvedValue(saved(['sources']));
    for (const gain of [1, 2, 3]) {
      commitSetting(
        'audio',
        { sources: [source({ gain })] },
        {
          field: 'audio:a:gain',
          debounceMs: AUTOSAVE_DEBOUNCE_MS,
        }
      );
      await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS / 2);
    }
    expect(store.stageSectionEdit).toHaveBeenCalledTimes(3);
    expect(store.saveStagedSection).not.toHaveBeenCalled();
    expect(autoSaveStatus('audio:a:gain').state).toBe('saving');

    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);
  });

  it('flushes a pending debounced value together with a toggle committed meanwhile', async () => {
    commitSetting(
      'audio',
      { sources: [source({ gain: 5 })] },
      {
        field: 'audio:a:gain',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    commitSetting(
      'audio',
      { sources: [source({ gain: 5, models: ['x'] })] },
      {
        field: 'audio:a:models',
      }
    );
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS * 3);
    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);
  });

  it('costs one more request, not one per commit, for commits made during a request', async () => {
    const first = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(first.promise);
    commitSetting('audio', { sources: [] }, { field: 'audio:sources:list' });
    await vi.advanceTimersByTimeAsync(0);
    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);

    commitSetting('audio', { sources: [source({ gain: 1 })] }, { field: 'audio:b:name' });
    commitSetting('audio', { sources: [source({ gain: 2 })] }, { field: 'audio:c:name' });
    first.resolve(saved(['sources']));
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(2);
  });

  it('shows an error for a failed save and clears a saved status after the visible time', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);

    expect(autoSaveStatus('audio:a:models')).toEqual({
      state: 'error',
      message: 'settings.autoSave.failed',
    });
    expect(autoSaveFailures()).toBe(1);
    expect(autoSaveBusy()).toBe(false);

    dismissAutoSaveError('audio:a:models');
    expect(autoSaveStatus('audio:a:models').state).toBe('idle');
    expect(autoSaveFailures()).toBe(0);

    store.saveStagedSection.mockResolvedValueOnce(saved(['sources']));
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);
    expect(autoSaveStatus('audio:a:models').state).toBe('saved');

    await vi.advanceTimersByTimeAsync(AUTOSAVE_SAVED_VISIBLE_MS);
    expect(autoSaveStatus('audio:a:models').state).toBe('idle');
  });

  it('marks only the fields that own a dropped path', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      saved: [['equalizer']],
      dropped: [['sources']],
    });
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    commitSetting(
      'audio',
      { equalizer: { enabled: true, filters: [] } },
      {
        field: 'audio:a:equalizer',
      }
    );
    await vi.advanceTimersByTimeAsync(0);

    expect(autoSaveStatus('audio:a:models')).toEqual({
      state: 'error',
      message: 'settings.autoSave.changedElsewhere',
    });
    expect(autoSaveStatus('audio:a:equalizer').state).toBe('saved');
  });

  it('marks the owning field when a refresh or load drops its path outside a save', async () => {
    const first = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(first.promise);
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    commitSetting(
      'rtsp',
      { streams: [] },
      {
        field: 'rtsp:s:enabled',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    await vi.advanceTimersByTimeAsync(0);

    dropListener?.([{ section: 'rtsp', path: ['streams'] }]);

    expect(autoSaveStatus('rtsp:s:enabled').state).toBe('error');
    expect(autoSaveStatus('audio:a:models').state).toBe('saving');
    first.resolve(saved(['sources']));
    await vi.advanceTimersByTimeAsync(0);
  });

  it('resolves flushAutoSaves only after a rerun and leaves nothing saving', async () => {
    const first = deferred<StagedSaveResult>();
    store.saveStagedSection
      .mockReturnValueOnce(first.promise)
      .mockResolvedValue(saved(['sources']));
    commitSetting('audio', { sources: [source({ gain: 1 })] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);
    commitSetting(
      'audio',
      { sources: [source({ gain: 2 })] },
      {
        field: 'audio:a:gain',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );

    let done = false;
    const flushed = flushAutoSaves().then(() => {
      done = true;
    });
    await vi.advanceTimersByTimeAsync(0);
    expect(done).toBe(false);

    first.resolve(saved(['sources']));
    await flushed;

    expect(store.saveStagedSection).toHaveBeenCalledTimes(2);
    expect(autoSaveBusy()).toBe(false);
    expect(autoSaveStatus('audio:a:gain').state).not.toBe('saving');
  });

  it('reports a refused stage as an error on the field without saving', async () => {
    store.stageSectionEdit.mockImplementationOnce(() => {
      throw new Error('settings.errors.loadFailed');
    });

    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).not.toHaveBeenCalled();
    expect(autoSaveStatus('audio:a:models').state).toBe('error');
  });
});
