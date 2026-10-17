import { describe, it, expect } from 'vitest';
import { appState, initializeFromConfig, type AppConfig } from './appState.svelte';

describe('appState - showAudioLevel', () => {
  it('defaults showAudioLevel to true', () => {
    expect(appState.showAudioLevel).toBe(true);
  });

  it('updates showAudioLevel when initializeFromConfig is called with false', () => {
    initializeFromConfig({ showAudioLevel: false } as AppConfig);
    expect(appState.showAudioLevel).toBe(false);
  });

  it('updates showAudioLevel when initializeFromConfig is called with true', () => {
    initializeFromConfig({ showAudioLevel: true } as AppConfig);
    expect(appState.showAudioLevel).toBe(true);
  });

  it('falls back to true when initializeFromConfig is called with undefined', () => {
    initializeFromConfig({} as AppConfig);
    expect(appState.showAudioLevel).toBe(true);
  });
});
