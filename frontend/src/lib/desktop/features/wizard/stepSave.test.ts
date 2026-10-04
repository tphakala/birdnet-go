import { describe, it, expect, vi, beforeEach } from 'vitest';

vi.mock('$lib/stores/settings', () => ({
  settingsActions: {
    saveSettings: vi.fn(),
    resetAllSettings: vi.fn(),
  },
}));

const { settingsActions } = await import('$lib/stores/settings');
const { saveStepSettings } = await import('./stepSave');

describe('saveStepSettings', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSettings).mockReset().mockResolvedValue(undefined);
    vi.mocked(settingsActions.resetAllSettings).mockReset();
  });

  it('applies the edits then saves without toasts', async () => {
    const order: string[] = [];
    const apply = vi.fn(() => order.push('apply'));
    vi.mocked(settingsActions.saveSettings).mockImplementation(async () => {
      order.push('save');
    });

    await saveStepSettings(apply, () => true);

    expect(order).toEqual(['apply', 'save']);
    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('reverts the store and rethrows when the save fails while the step is current', async () => {
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSettings).mockRejectedValue(failure);

    await expect(
      saveStepSettings(
        () => {},
        () => true
      )
    ).rejects.toBe(failure);

    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('does not revert after the step unmounted but still rethrows', async () => {
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockRejectedValue(failure);

    await expect(
      saveStepSettings(
        () => {},
        () => false
      )
    ).rejects.toBe(failure);

    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });

  it('does not revert on success', async () => {
    await saveStepSettings(
      () => {},
      () => true
    );

    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });
});
