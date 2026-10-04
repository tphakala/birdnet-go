import { describe, it, expect } from 'vitest';
import {
  resolveWizardLaunch,
  type SettingsLoadState,
  type WizardLaunchDecision,
} from './wizardLaunch';

const VERSION = 'v2.0';

function decide(
  overrides: Partial<Parameters<typeof resolveWizardLaunch>[0]>
): WizardLaunchDecision {
  return resolveWizardLaunch({
    dismissedVersion: null,
    version: VERSION,
    freshInstall: false,
    newVersion: false,
    settingsLoad: 'pending',
    ...overrides,
  });
}

const loadStates: SettingsLoadState[] = ['pending', 'loaded', 'failed', 'skipped'];

describe('resolveWizardLaunch', () => {
  it.each([
    ['pending', 'wait'],
    ['loaded', 'onboarding'],
    ['failed', 'none'],
    ['skipped', 'none'],
  ] as const)('fresh install with settings %s resolves to %s', (settingsLoad, expected) => {
    expect(decide({ freshInstall: true, settingsLoad })).toBe(expected);
  });

  it.each(loadStates)('a dismissed version wins over everything with settings %s', settingsLoad => {
    expect(
      decide({ dismissedVersion: VERSION, freshInstall: true, newVersion: true, settingsLoad })
    ).toBe('none');
  });

  it('ignores a dismissal recorded for another version', () => {
    expect(decide({ dismissedVersion: 'v1.0', freshInstall: true, settingsLoad: 'loaded' })).toBe(
      'onboarding'
    );
  });

  it.each(loadStates)('whats-new does not depend on settings (%s)', settingsLoad => {
    expect(decide({ newVersion: true, settingsLoad })).toBe('whats-new');
  });

  it('prefers onboarding over whats-new on a fresh install', () => {
    expect(decide({ freshInstall: true, newVersion: true, settingsLoad: 'loaded' })).toBe(
      'onboarding'
    );
  });

  it.each(loadStates)('neither fresh nor new resolves to none (%s)', settingsLoad => {
    expect(decide({ settingsLoad })).toBe('none');
  });

  it('pending then loaded yields wait then onboarding', () => {
    expect(decide({ freshInstall: true, settingsLoad: 'pending' })).toBe('wait');
    expect(decide({ freshInstall: true, settingsLoad: 'loaded' })).toBe('onboarding');
  });

  it('pending then failed yields wait then none', () => {
    expect(decide({ freshInstall: true, settingsLoad: 'pending' })).toBe('wait');
    expect(decide({ freshInstall: true, settingsLoad: 'failed' })).toBe('none');
  });
});
