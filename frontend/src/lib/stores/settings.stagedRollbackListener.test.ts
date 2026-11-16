/**
 * A store subscriber that throws while a failed staged save is taken back must
 * not make saveStagedSection reject, and the reverted paths are still settled.
 *
 * This lives in its own file because a throwing subscriber leaves svelte/store's
 * module-level notify queue stuck, so no store in the same module instance
 * notifies its subscribers again; any test after it in a shared file would
 * silently see no updates.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { get } from 'svelte/store';
import {
  settingsStore,
  settingsActions,
  resetIfMatchFallbackForTest,
  resetStagedEditsForTest,
} from './settings';
import type { SettingsFormData } from './settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { mergeSettingsPatch } from '$lib/utils/settingsMerge';
import { serverSettings } from '../../test/settings-helpers';

vi.mock('$lib/utils/settingsApi.js', () => ({
  settingsAPI: {
    load: vi.fn(),
    save: vi.fn().mockResolvedValue(undefined),
    patchSection: vi.fn().mockResolvedValue({}),
  },
}));

vi.mock('./toast.js', () => ({
  toastActions: { success: vi.fn(), error: vi.fn() },
}));

vi.mock('$lib/i18n/index.js', () => ({
  getLocale: vi.fn().mockReturnValue('en'),
  setLocale: vi.fn(),
  isValidLocale: vi.fn().mockReturnValue(true),
  t: vi.fn((key: string) => key),
}));

type Rec = Record<string, unknown>;

/** The server always emits quietHours on a source; a client never sends it. */
const inServerShape = (sources: Rec[]): Rec[] =>
  sources.map(source => ({ quietHours: { enabled: false }, ...source }));
const sources = (name: string) => [{ name, device: 'hw:0', gain: 0, models: ['birdnet'] }];
const formAudio = () => get(settingsStore).formData.realtime?.audio as unknown as Rec;

describe('Settings Store - failed staged save with a throwing store listener', () => {
  let server: Rec;

  beforeEach(async () => {
    resetIfMatchFallbackForTest();
    resetStagedEditsForTest();
    server = serverSettings() as unknown as Rec;
    vi.mocked(settingsAPI.load)
      .mockReset()
      .mockImplementation(async () => JSON.parse(JSON.stringify(server)));
    vi.mocked(settingsAPI.patchSection)
      .mockReset()
      .mockImplementation(async (_section, body: Rec) => {
        const realtime = server.realtime as Rec;
        const audio = mergeSettingsPatch(realtime.audio as Rec, body);
        if (Array.isArray(audio.sources)) audio.sources = inServerShape(audio.sources as Rec[]);
        realtime.audio = audio;
        return { settings: JSON.parse(JSON.stringify(audio)), skippedFields: [] };
      });
    settingsStore.set({
      formData: { main: { name: '' } } as unknown as SettingsFormData,
      originalData: { main: { name: '' } } as unknown as SettingsFormData,
      isLoading: false,
      isSaving: false,
      activeSection: 'main',
      error: null,
      dataLoaded: false,
    });
    await settingsActions.loadSettings();
  });

  afterEach(() => {
    resetStagedEditsForTest();
  });

  it('returns its result and settles the paths when a listener throws during the rollback', async () => {
    vi.mocked(settingsAPI.patchSection).mockRejectedValueOnce(
      Object.assign(new Error('Bad Request'), { status: 400 })
    );
    const stored = formAudio().sources;
    settingsActions.stageSectionEdit('audio', { sources: sources('A') });
    let armed = true;
    const unsubscribe = settingsStore.subscribe(state => {
      const shown = state.formData.realtime?.audio?.sources;
      if (armed && JSON.stringify(shown) === JSON.stringify(stored)) {
        armed = false;
        throw new Error('listener failed');
      }
    });

    const result = await settingsActions.saveStagedSection('audio');
    unsubscribe();

    expect(result.error).toBeInstanceOf(Error);
    expect(result.failed).toEqual([['sources']]);
    expect(result.saved).toEqual([]);
    expect(result.remaining).toBe(false);
    expect(formAudio().sources).toEqual(stored);
  });
});
