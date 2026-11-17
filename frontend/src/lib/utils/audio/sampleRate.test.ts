import { describe, it, expect } from 'vitest';
import {
  coerceSupportedRate,
  DEFAULT_SAMPLE_RATE,
  highestMinSampleRate,
  type SampleRateOption,
} from './sampleRate';

const opt = (rate: number): SampleRateOption => ({
  value: String(rate),
  label: `${rate / 1000} kHz`,
});

describe('coerceSupportedRate', () => {
  it('keeps the current rate when the device supports it', () => {
    const options = [opt(48000), opt(96000), opt(192000)];
    expect(coerceSupportedRate(options, 96000)).toBe(96000);
  });

  it('falls back to 48 kHz when the current rate is unsupported but 48 kHz is offered', () => {
    const options = [opt(48000), opt(192000), opt(384000)];
    expect(coerceSupportedRate(options, 96000)).toBe(48000);
  });

  it('falls back to the first supported rate when 48 kHz is not offered', () => {
    // A specialised interface that only supports 96 kHz and up.
    const options = [opt(96000), opt(192000)];
    expect(coerceSupportedRate(options, 48000)).toBe(96000);
  });

  it('returns the default when the option list is empty', () => {
    expect(coerceSupportedRate([], 192000)).toBe(DEFAULT_SAMPLE_RATE);
  });
});

describe('highestMinSampleRate', () => {
  const models = [
    { id: 'birdnet' },
    { id: 'bat', minSampleRate: 96000 },
    { id: 'bsgbat', minSampleRate: 192000 },
  ];

  it('returns 0 when no selected model has a minimum', () => {
    expect(highestMinSampleRate(models, ['birdnet'])).toBe(0);
  });

  it('returns the minimum of a single selected model', () => {
    expect(highestMinSampleRate(models, ['birdnet', 'bat'])).toBe(96000);
    expect(highestMinSampleRate(models, ['bsgbat'])).toBe(192000);
  });

  it('returns the highest minimum when several selected models have one', () => {
    expect(highestMinSampleRate(models, ['bat', 'bsgbat'])).toBe(192000);
  });

  it('ignores models that are not selected and ids the list does not know', () => {
    expect(highestMinSampleRate(models, ['bat', 'unknown'])).toBe(96000);
    expect(highestMinSampleRate(models, [])).toBe(0);
  });
});
