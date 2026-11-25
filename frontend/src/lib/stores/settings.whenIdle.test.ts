/**
 * settingsActions.whenIdle resolves once every save and load the store queued has settled,
 * including work queued while it waited.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  settingsStore,
  settingsActions,
  resetIfMatchFallbackForTest,
  resetStagedEditsForTest,
} from './settings';
import type { SettingsFormData } from './settings';
import { settingsAPI } from '$lib/utils/settingsApi.js';
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

function deferred<T>() {
  let resolve: (value: T) => void = () => {};
  const promise = new Promise<T>(r => {
    resolve = r;
  });
  return { promise, resolve };
}

async function settled(promise: Promise<unknown>): Promise<boolean> {
  let done = false;
  void promise.then(() => {
    done = true;
  });
  await new Promise(resolve => setTimeout(resolve, 0));
  return done;
}

describe('Settings Store - whenIdle', () => {
  let server: Rec;

  beforeEach(async () => {
    resetIfMatchFallbackForTest();
    resetStagedEditsForTest();
    server = serverSettings() as unknown as Rec;
    vi.mocked(settingsAPI.load)
      .mockReset()
      .mockImplementation(async () => JSON.parse(JSON.stringify(server)));
    vi.mocked(settingsAPI.save).mockReset().mockResolvedValue(undefined);
    // Deliberately partial fixture: only main.name is edited here, and loadSettings()
    // below loads the full settings from the mocked API.
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

  it('resolves at once when nothing is queued', async () => {
    expect(await settled(settingsActions.whenIdle())).toBe(true);
  });

  it('waits for a save and for a second save queued behind it', async () => {
    const first = deferred<undefined>();
    const second = deferred<undefined>();
    vi.mocked(settingsAPI.save)
      .mockReturnValueOnce(first.promise)
      .mockReturnValueOnce(second.promise);
    settingsStore.update(state => ({
      ...state,
      formData: { ...state.formData, main: { name: 'a' } },
    }));
    const firstSave = settingsActions.saveSettings({ notify: false });
    const secondSave = settingsActions.saveSettings({ notify: false });
    const idle = settingsActions.whenIdle();

    first.resolve(undefined);
    await firstSave.catch(() => undefined);
    // isSaving is false between the two saves, but the second one is still queued.
    expect(await settled(idle)).toBe(false);

    second.resolve(undefined);
    await secondSave.catch(() => undefined);
    expect(await settled(idle)).toBe(true);
  });
});
