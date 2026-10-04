import { vi } from 'vitest';
import { writable } from 'svelte/store';
import type { Component } from 'svelte';
import type { SettingsFormData } from '$lib/stores/settings';
import { renderTyped } from '../../../../../test/render-helpers';
import type { StepLeaveHandler, WizardStepProps } from '../types';

/**
 * Renders a wizard step with a registerLeaveHandler spy and exposes the handler
 * it registered as leave().
 */
export function renderStep(StepComponent: Component<WizardStepProps>) {
  let handler: StepLeaveHandler | undefined;
  const unregister = vi.fn();
  const registerLeaveHandler = vi.fn((h: StepLeaveHandler) => {
    handler = h;
    return unregister;
  });
  const result = renderTyped(StepComponent, { props: { registerLeaveHandler } });
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

/**
 * Flushes pending microtasks such as onMount continuations. Two turns cover the
 * follow-up microtask Svelte's effects and onMount initializers commonly chain.
 */
export async function flushAsync(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

interface SettingsMockOptions {
  /** Merge updateSection calls into the store's formData. Defaults to false. */
  applyUpdates?: boolean;
}

/**
 * Builds a `$lib/stores/settings` mock for step tests: a writable store seeded
 * with formData, and spied updateSection, saveSettings and resetAllSettings.
 * Use it from a vi.mock factory through a dynamic import, since vi.mock is
 * hoisted above static imports.
 */
export function createSettingsMock(formData: unknown, options: SettingsMockOptions = {}) {
  const { applyUpdates = false } = options;
  const clone = () => JSON.parse(JSON.stringify(formData)) as SettingsFormData;
  const settingsStore = writable({
    isLoading: false,
    isSaving: false,
    error: null,
    dataLoaded: true,
    activeSection: 'main',
    originalData: clone(),
    formData: clone(),
  });
  const updateSection = vi.fn((section: string, data: Record<string, unknown>) => {
    if (!applyUpdates) return;
    settingsStore.update(state => {
      const sections = state.formData as unknown as Record<string, Record<string, unknown>>;
      // eslint-disable-next-line security/detect-object-injection -- Safe: test mock with controlled section keys
      sections[section] = { ...(sections[section] ?? {}), ...data };
      return state;
    });
  });
  return {
    settingsStore,
    settingsActions: {
      updateSection,
      saveSettings: vi.fn().mockResolvedValue(undefined),
      resetAllSettings: vi.fn(),
    },
  };
}
