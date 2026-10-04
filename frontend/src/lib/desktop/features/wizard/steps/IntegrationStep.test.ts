import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import type { SettingsFormData } from '$lib/stores/settings';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { writable } = await vi.importActual<typeof import('svelte/store')>('svelte/store');
  const formData = {
    realtime: { privacyFilter: { enabled: true }, birdweather: { enabled: false, id: '' } },
    sentry: { enabled: false },
  } as unknown as SettingsFormData;
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

import IntegrationStep from './IntegrationStep.svelte';
import { settingsActions } from '$lib/stores/settings';
import type { StepLeaveHandler } from '../types';

function renderStep() {
  let handler: StepLeaveHandler | undefined;
  const unregister = vi.fn();
  const registerLeaveHandler = vi.fn((h: StepLeaveHandler) => {
    handler = h;
    return unregister;
  });
  const result = render(IntegrationStep, { props: { registerLeaveHandler } });
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
    screen.getByRole('button', { name: /wizard\.steps\.integration\.errorReportingLabel/ })
  );
}

describe('IntegrationStep - leave handler', () => {
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

    expect(settingsActions.updateSection).toHaveBeenCalledTimes(2);
    expect(settingsActions.updateSection).toHaveBeenCalledWith('realtime', {
      privacyFilter: { enabled: true },
      birdweather: { enabled: false, id: '' },
    });
    expect(settingsActions.updateSection).toHaveBeenCalledWith('sentry', { enabled: true });
    expect(settingsActions.saveSettings).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSettings).toHaveBeenCalledWith({ notify: false });
  });
});
