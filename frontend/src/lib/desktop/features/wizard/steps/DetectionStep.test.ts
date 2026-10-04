import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import type { SettingsFormData } from '$lib/stores/settings';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const formData = { birdnet: { threshold: 0.8 } } as unknown as SettingsFormData;
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    dataLoaded: true,
    activeSection: 'main',
    originalData: formData,
    formData,
  });
  return {
    settingsStore,
    settingsActions: {
      updateSection: vi.fn(),
      saveSettings: vi.fn().mockResolvedValue(undefined),
      resetAllSettings: vi.fn(),
    },
  };
});

import DetectionStep from './DetectionStep.svelte';
import { settingsActions } from '$lib/stores/settings';
import type { StepLeaveHandler } from '../types';

function renderStep() {
  let handler: StepLeaveHandler | undefined;
  const unregister = vi.fn();
  const registerLeaveHandler = vi.fn((h: StepLeaveHandler) => {
    handler = h;
    return unregister;
  });
  const result = render(DetectionStep, { props: { registerLeaveHandler } });
  return {
    ...result,
    registerLeaveHandler,
    unregister,
    leave: () => {
      if (!handler) throw new Error('leave handler was not registered');
      return handler();
    },
  };
}

async function flushAsync() {
  await Promise.resolve();
  await Promise.resolve();
}

async function edit() {
  await fireEvent.click(
    screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
  );
}

describe('DetectionStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.updateSection).mockClear();
    vi.mocked(settingsActions.saveSettings).mockClear().mockResolvedValue(undefined);
    vi.mocked(settingsActions.resetAllSettings).mockClear();
  });

  it('registers a leave handler on mount and unregisters on destroy', async () => {
    const { registerLeaveHandler, unregister, unmount } = renderStep();
    await flushAsync();

    expect(registerLeaveHandler).toHaveBeenCalledTimes(1);
    expect(unregister).not.toHaveBeenCalled();

    unmount();

    expect(unregister).toHaveBeenCalledTimes(1);
  });

  it('unmounting never saves', async () => {
    const { unmount } = renderStep();
    await flushAsync();
    await edit();

    unmount();
    await flushAsync();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler does nothing without edits', async () => {
    const { leave } = renderStep();
    await flushAsync();

    await leave();

    expect(settingsActions.updateSection).not.toHaveBeenCalled();
    expect(settingsActions.saveSettings).not.toHaveBeenCalled();
  });

  it('the leave handler rejects when the save rejects', async () => {
    const failure = new Error('save failed');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);
    const { leave } = renderStep();
    await flushAsync();
    await edit();

    await expect(leave()).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).toHaveBeenCalledTimes(1);
  });

  it('the leave handler saves the edited values once', async () => {
    const { leave } = renderStep();
    await flushAsync();
    await edit();

    await leave();
    await leave();

    expect(settingsActions.updateSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.updateSection).toHaveBeenCalledWith('birdnet', { threshold: 0.9 });
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });

  it('does not revert the store when the save fails after the step unmounted', async () => {
    const failure = new Error('late failure');
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(failure);
    const { leave, unmount } = renderStep();
    await flushAsync();
    await edit();

    const pending = leave();
    unmount();

    await expect(pending).rejects.toBe(failure);
    expect(settingsActions.resetAllSettings).not.toHaveBeenCalled();
  });

  it('keeps the edits after a failed save so the next leave call retries them', async () => {
    vi.mocked(settingsActions.saveSettings).mockRejectedValueOnce(new Error('save failed'));
    const { leave } = renderStep();
    await flushAsync();
    await edit();

    await expect(leave()).rejects.toThrow('save failed');
    await leave();

    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(2);
    const calls = vi.mocked(settingsActions.updateSection).mock.calls;
    expect(calls.length).toBeGreaterThanOrEqual(2);
    expect(calls.slice(0, calls.length / 2)).toEqual(calls.slice(calls.length / 2));
  });
});
