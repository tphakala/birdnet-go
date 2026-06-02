import { describe, it, expect } from 'vitest';
import { normalizeChannelMode, channelUIState, sampleRateKhz } from './streamChannel';

describe('sampleRateKhz', () => {
  it('converts 48000 Hz to 48 kHz with no trailing zeros', () => {
    expect(sampleRateKhz(48000)).toBe(48);
  });

  it('keeps one decimal for 44100 Hz (44.1 kHz)', () => {
    expect(sampleRateKhz(44100)).toBe(44.1);
  });

  it('converts 16000 Hz to 16 kHz', () => {
    expect(sampleRateKhz(16000)).toBe(16);
  });
});

describe('normalizeChannelMode', () => {
  it('maps an empty string to downmix (the documented default)', () => {
    expect(normalizeChannelMode('')).toBe('downmix');
  });

  it('maps undefined to downmix', () => {
    expect(normalizeChannelMode(undefined)).toBe('downmix');
  });

  it('maps null to downmix', () => {
    expect(normalizeChannelMode(null)).toBe('downmix');
  });

  it('maps an unknown value to downmix', () => {
    expect(normalizeChannelMode('stereo')).toBe('downmix');
  });

  it('preserves explicit downmix', () => {
    expect(normalizeChannelMode('downmix')).toBe('downmix');
  });

  it('preserves left', () => {
    expect(normalizeChannelMode('left')).toBe('left');
  });

  it('preserves right', () => {
    expect(normalizeChannelMode('right')).toBe('right');
  });
});

describe('channelUIState', () => {
  it('hides the selector for an untested stream (0 channels, default mode)', () => {
    expect(channelUIState(0, 'downmix')).toEqual({
      showSelector: false,
      showAnalysis: false,
      isMono: false,
    });
  });

  it('treats a mono stream as single-channel: no selector, no analysis', () => {
    expect(channelUIState(1, 'downmix')).toEqual({
      showSelector: false,
      showAnalysis: false,
      isMono: true,
    });
  });

  it('shows the full selector and analysis for a multi-channel stream', () => {
    expect(channelUIState(2, 'downmix')).toEqual({
      showSelector: true,
      showAnalysis: true,
      isMono: false,
    });
  });

  it('shows the selector for a stream explicitly set to left even when offline (0 channels)', () => {
    // A configured left/right stream must remain editable when no live probe exists,
    // otherwise the user cannot review or change a setting they already made.
    expect(channelUIState(0, 'left')).toEqual({
      showSelector: true,
      showAnalysis: false,
      isMono: false,
    });
  });

  it('shows the selector but not analysis for a right-configured stream that probes as mono', () => {
    expect(channelUIState(1, 'right')).toEqual({
      showSelector: true,
      showAnalysis: false,
      isMono: false,
    });
  });

  it('shows selector and analysis for a multi-channel stream set to left', () => {
    expect(channelUIState(2, 'left')).toEqual({
      showSelector: true,
      showAnalysis: true,
      isMono: false,
    });
  });
});
