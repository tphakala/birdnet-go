import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await import('svelte/store');
  return {
    settingsStore: writable({ formData: { birdnet: { threshold: 0.8 } } }),
    settingsActions: {
      saveSettings: vi.fn(),
      resetAllSettings: vi.fn(),
    },
  };
});

const { settingsActions, settingsStore } = await import('$lib/stores/settings');
const { saveStepSettings } = await import('./stepSave');

describe('saveStepSettings', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSettings).mockReset().mockResolvedValue(undefined);
    vi.mocked(settingsActions.resetAllSettings).mockReset();
  });

  it('saves without toasts', async () => {
    await saveStepSettings(() => true);

    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('reverts the store and rethrows when the save fails while the step is current', async () => {
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSettings).mockRejectedValue(failure);

    await expect(saveStepSettings(() => true)).rejects.toBe(failure);

    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('reverts after the step is gone while the store still holds what was submitted', async () => {
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockRejectedValue(failure);

    await expect(saveStepSettings(() => false)).rejects.toBe(failure);

    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('keeps later store edits when the save fails after the step is gone', async () => {
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockImplementation(async () => {
      settingsStore.update(state => ({
        ...state,
        formData: { ...state.formData, birdnet: { ...state.formData.birdnet, threshold: 0.5 } },
      }));
      throw failure;
    });

    await expect(saveStepSettings(() => false)).rejects.toBe(failure);

    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });

  it('does not revert on success', async () => {
    await saveStepSettings(() => true);

    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });
});
