import { describe, it, expect } from 'vitest';
import { appState, initializeFromConfig, type AppConfig } from './appState.svelte';

describe('appState - showAudioLevel', () => {
  it('defaults showAudioLevel to true', () => {
    expect(appState.showAudioLevel).toBe(true);
  });

  it('updates showAudioLevel when initializeFromConfig is called with false', () => {
    const config: Partial<AppConfig> = { showAudioLevel: false };
    initializeFromConfig(config);
    expect(appState.showAudioLevel).toBe(false);
  });

  it('updates showAudioLevel when initializeFromConfig is called with true', () => {
    const config: Partial<AppConfig> = { showAudioLevel: true };
    initializeFromConfig(config);
    expect(appState.showAudioLevel).toBe(true);
  });

  it('falls back to true when initializeFromConfig is called with undefined', () => {
    const config: Partial<AppConfig> = {};
    initializeFromConfig(config);
    expect(appState.showAudioLevel).toBe(true);
  });
});
