import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { AudioSourceConfig, StagedSaveResult, DroppedStagedEdit } from './settings';
import { deferred } from '../../test/async-helpers';
import { ApiError } from '$lib/utils/api';
import { SettingsChangedElsewhereError } from './settings';

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
  registerAutoSaveField,
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

  it('marks only the fields that own a dropped path, in one shared request', async () => {
    store.saveStagedSection.mockResolvedValueOnce({
      ...OK,
      saved: [['equalizer']],
      dropped: [['sources']],
    });
    commitSetting(
      'audio',
      { sources: [] },
      {
        field: 'audio:a:models',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    commitSetting(
      'audio',
      { equalizer: { enabled: true, filters: [] } },
      {
        field: 'audio:a:equalizer',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);
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

  it('sends a commit that lands while the finishing flush reads its result', async () => {
    const result: StagedSaveResult = {
      saved: [['sources']],
      dropped: [],
      failed: [],
      get remaining() {
        commitSetting('audio', { sources: [source({ gain: 9 })] }, { field: 'audio:b:gain' });
        return false;
      },
    };
    store.saveStagedSection.mockResolvedValueOnce(result);

    commitSetting('audio', { sources: [source()] }, { field: 'audio:a:gain' });
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(2);
    expect(autoSaveBusy()).toBe(false);
  });

  it('shows an error instead of staying on Saving when the save promise rejects', async () => {
    store.saveStagedSection.mockRejectedValueOnce(new Error('network down'));

    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);

    expect(autoSaveStatus('audio:a:models').state).toBe('error');
    expect(autoSaveBusy()).toBe(false);
  });

  it('keeps the error of a refused commit when an earlier commit of the field is still pending', async () => {
    commitSetting(
      'audio',
      { sources: [source({ gain: 1 })] },
      {
        field: 'audio:a:gain',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    store.stageSectionEdit.mockImplementationOnce(() => {
      throw new Error('settings.errors.loadFailed');
    });
    commitSetting(
      'audio',
      { sources: [source({ gain: 2 })] },
      {
        field: 'audio:a:gain',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );
    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS * 2);

    expect(autoSaveStatus('audio:a:gain').state).toBe('error');
  });

  it('keeps a field on Saving while a newer commit of it waits for the next request', async () => {
    const first = deferred<StagedSaveResult>();
    const second = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    commitSetting('audio', { sources: [source({ gain: 1 })] }, { field: 'audio:a:gain' });
    await vi.advanceTimersByTimeAsync(0);
    commitSetting('audio', { sources: [source({ gain: 2 })] }, { field: 'audio:a:gain' });

    first.resolve(saved(['sources']));
    await vi.advanceTimersByTimeAsync(0);
    expect(autoSaveStatus('audio:a:gain').state).toBe('saving');

    second.resolve(saved(['sources']));
    await vi.advanceTimersByTimeAsync(0);
    expect(autoSaveStatus('audio:a:gain').state).toBe('saved');
  });

  it('holds a debounced commit made during a request until its quiet time ends', async () => {
    const first = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(first.promise);
    commitSetting('audio', { sources: [source()] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);
    commitSetting(
      'audio',
      { sources: [source({ gain: 4 })] },
      {
        field: 'audio:a:gain',
        debounceMs: AUTOSAVE_DEBOUNCE_MS,
      }
    );

    first.resolve({ ...saved(['sources']), remaining: true });
    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS / 2);
    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);

    await vi.advanceTimersByTimeAsync(AUTOSAVE_DEBOUNCE_MS);
    expect(store.saveStagedSection).toHaveBeenCalledTimes(2);
  });

  it('does not loop on a result that is not done but made no progress', async () => {
    store.saveStagedSection.mockResolvedValue({ ...OK, remaining: true });

    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(1);
    expect(autoSaveBusy()).toBe(false);
  });

  it('shows Saved when the section had nothing to send', async () => {
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);

    expect(autoSaveStatus('audio:a:models').state).toBe('saved');
  });

  it('shows the server message of an error with a key and the conflict text of a conflict', async () => {
    const serverError = new ApiError('Name is already used', 400, new Response());
    serverError.errorKey = 'settings.errors.duplicateName';
    store.saveStagedSection
      .mockResolvedValueOnce({ ...OK, failed: [['sources']], error: serverError })
      .mockResolvedValueOnce({
        ...OK,
        failed: [['equalizer']],
        error: new SettingsChangedElsewhereError(),
      });

    commitSetting('audio', { sources: [] }, { field: 'audio:a:name' });
    await vi.advanceTimersByTimeAsync(0);
    commitSetting(
      'audio',
      { equalizer: { enabled: true, filters: [] } },
      {
        field: 'audio:a:equalizer',
      }
    );
    await vi.advanceTimersByTimeAsync(0);

    expect(autoSaveStatus('audio:a:name')).toEqual({
      state: 'error',
      message: 'Name is already used',
    });
    expect(autoSaveStatus('audio:a:equalizer')).toEqual({
      state: 'error',
      message: 'settings.errors.changedElsewhere',
    });
  });

  it('sends a commit made during a request that then rejects, and a field committed again keeps Saving', async () => {
    const first = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(first.promise);
    commitSetting('audio', { sources: [source()] }, { field: 'audio:a:models' });
    await vi.advanceTimersByTimeAsync(0);
    commitSetting('audio', { sources: [source({ gain: 3 })] }, { field: 'audio:b:gain' });
    commitSetting('audio', { sources: [source({ gain: 4 })] }, { field: 'audio:a:models' });

    first.reject(new Error('network down'));
    await vi.advanceTimersByTimeAsync(0);

    expect(store.saveStagedSection).toHaveBeenCalledTimes(2);
    expect(autoSaveStatus('audio:a:models').state).toBe('saved');
    expect(autoSaveStatus('audio:b:gain').state).toBe('saved');
  });

  it('scopes busy and failure counts to a field key prefix', async () => {
    store.saveStagedSection.mockResolvedValue({
      ...OK,
      failed: [['sources']],
      error: new Error('boom'),
    });
    commitSetting('audio', { sources: [source()] }, { field: 'audio:sources:a:gain' });
    await flushAutoSaves();

    expect(autoSaveFailures()).toBe(1);
    expect(autoSaveFailures('audio:sources:')).toBe(1);
    expect(autoSaveFailures('rtsp:')).toBe(0);

    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    commitSetting('rtsp', { streams: [] }, { field: 'rtsp:s:name' });
    expect(autoSaveBusy('rtsp:')).toBe(true);
    expect(autoSaveBusy('audio:sources:')).toBe(false);
    pending.resolve(OK);
    await flushAutoSaves();
  });

  it('does not count an error that arrives after its control unmounted, and shows it again on remount', async () => {
    const pending = deferred<StagedSaveResult>();
    store.saveStagedSection.mockReturnValueOnce(pending.promise);
    const release = registerAutoSaveField('audio:a:models');
    commitSetting('audio', { sources: [source()] }, { field: 'audio:a:models' });
    release();
    // A request still running keeps the field busy.
    expect(autoSaveBusy()).toBe(true);

    pending.resolve({ ...OK, failed: [['sources']], error: new Error('boom') });
    await flushAutoSaves();
    expect(autoSaveFailures()).toBe(0);

    const releaseAgain = registerAutoSaveField('audio:a:models');
    expect(autoSaveStatus('audio:a:models').state).toBe('error');
    expect(autoSaveFailures()).toBe(1);
    releaseAgain();
  });
});
