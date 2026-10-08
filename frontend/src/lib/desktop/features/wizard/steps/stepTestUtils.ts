import { vi } from 'vitest';
import { screen } from '@testing-library/svelte';
import { writable } from 'svelte/store';
import type { Component } from 'svelte';
import type { SettingsFormData } from '$lib/stores/settings';
import { renderTyped } from '../../../../../test/render-helpers';
import type { StepLeaveHandler, WizardStepProps } from '../types';

/**
 * Renders a wizard step with a registerLeaveHandler spy and exposes the handler
 * it registered as leave(). Extra props, such as an onValidChange spy, are merged
 * into the step's props.
 */
export function renderStep(
  StepComponent: Component<WizardStepProps>,
  props?: Omit<WizardStepProps, 'registerLeaveHandler'>
) {
  let handler: StepLeaveHandler | undefined;
  const unregister = vi.fn();
  const registerLeaveHandler = vi.fn((h: StepLeaveHandler) => {
    handler = h;
    return unregister;
  });
  const result = renderTyped(StepComponent, { props: { ...props, registerLeaveHandler } });
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

/**
 * Builds a `$lib/stores/settings` mock for step tests: a writable store seeded
 * with formData, a spied saveSection, plus updateSection and saveSettings so
 * tests can assert the wizard never calls them. Use it from a vi.mock factory
 * through a dynamic import, since vi.mock is hoisted above static imports.
 */
export function createSettingsMock(formData: unknown) {
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
  return {
    settingsStore,
    StreamTypes: { RTSP: 'rtsp' },
    // Same shape as the real constant, which audioSourceChoice.ts imports
    defaultQuietHoursConfig: {
      enabled: false,
      mode: 'fixed',
      startTime: '22:00',
      endTime: '06:00',
      startEvent: 'sunset',
      startOffset: 0,
      endEvent: 'sunrise',
      endOffset: 0,
    },
    settingsActions: {
      saveSection: vi.fn().mockResolvedValue(undefined),
      updateSection: vi.fn(),
      saveSettings: vi.fn().mockResolvedValue(undefined),
    },
  };
}

/** The step's option card with the given accessible name. */
export const radio = (name: RegExp) => screen.getByRole('radio', { name });
