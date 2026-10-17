import { describe, it, expect } from 'vitest';
import fallbackEqFilterConfig from '$lib/desktop/features/settings/components/eqFilterFallbackConfig.json';
import {
  DEFAULT_EQ_Q,
  DEFAULT_EQ_WIDTH_HZ,
  defaultEqFrequencyHz,
  EQ_DB_PER_PASS,
  EQ_GENERIC_FREQUENCY_HZ,
  EQ_Q_INPUT_STEP,
  eqInputStep,
  EQ_OFFERED_MAX_PASSES,
  EQ_PASSES_OFF,
  resolveNumericEntry,
  type NumericEntryNotice,
} from './equalizer';

// The fallback table is a copy of the backend table, pinned to it by a Go test
// (TestEqFilterConfigMatchesFrontendFallback), so these pins reach the backend.
const TYPES = Object.entries(fallbackEqFilterConfig);

// Allowed floating-point error when checking that a value is a whole number of steps
const STEP_TOLERANCE = 1e-9;

function paramsNamed(name: string) {
  return TYPES.flatMap(([type, config]) =>
    config.Parameters.filter(p => p.Name === name).map(p => ({ type, param: p }))
  );
}

describe('equalizer constants', () => {
  it('DEFAULT_EQ_Q equals the Q default of every filter type in the fallback table', () => {
    const qParams = paramsNamed('Q');
    expect(qParams.length).toBeGreaterThan(0);
    for (const { param } of qParams) {
      expect(param.Default).toBe(DEFAULT_EQ_Q);
    }
  });

  it('DEFAULT_EQ_WIDTH_HZ equals the BandReject width default', () => {
    const width = fallbackEqFilterConfig.BandReject.Parameters.find(p => p.Name === 'Width');
    expect(width?.Default).toBe(DEFAULT_EQ_WIDTH_HZ);
  });

  it('EQ_OFFERED_MAX_PASSES equals the Passes maximum of every type', () => {
    const passes = paramsNamed('Passes');
    expect(passes).toHaveLength(TYPES.length);
    for (const { param } of passes) {
      expect(param.Max).toBe(EQ_OFFERED_MAX_PASSES);
      expect(param.Min).toBe(EQ_PASSES_OFF);
    }
  });

  it('puts the Q default on the Q input step grid, which starts at the Q minimum', () => {
    for (const { param } of paramsNamed('Q')) {
      const steps = (DEFAULT_EQ_Q - param.Min) / eqInputStep('Q');
      expect(Math.abs(steps - Math.round(steps))).toBeLessThan(STEP_TOLERANCE);
    }
    expect(eqInputStep('Q')).toBe(EQ_Q_INPUT_STEP);
    for (const name of ['Frequency', 'Width', 'Gain'] as const) {
      expect(eqInputStep(name)).toBeGreaterThan(0);
    }
  });
});

describe('defaultEqFrequencyHz', () => {
  it("matches the fallback table's Frequency default for every type in it", () => {
    for (const { type, param } of paramsNamed('Frequency')) {
      expect(defaultEqFrequencyHz(type)).toBe(param.Default);
    }
  });

  it('gives BandPass the BandReject default and an unknown type the generic default', () => {
    expect(defaultEqFrequencyHz('BandPass')).toBe(defaultEqFrequencyHz('BandReject'));
    expect(defaultEqFrequencyHz('Peaking')).toBe(EQ_GENERIC_FREQUENCY_HZ);
    expect(defaultEqFrequencyHz('')).toBe(EQ_GENERIC_FREQUENCY_HZ);
  });
});

describe('EQ_DB_PER_PASS', () => {
  it('matches the attenuation steps the table tooltip names (1 pass = 12 dB)', () => {
    expect(EQ_DB_PER_PASS).toBe(12);
    for (const { param } of paramsNamed('Passes')) {
      expect(param.Tooltip).toContain(`1=${EQ_DB_PER_PASS}dB`);
    }
  });
});

describe('resolveNumericEntry', () => {
  const MIN = 0.1;
  const MAX = 10;
  const CURRENT = 0.707;
  const STEP = 0.001;

  const CASES: Array<{
    name: string;
    raw: string;
    value: number;
    notice: NumericEntryNotice | null;
  }> = [
    { name: 'in range', raw: '2.5', value: 2.5, notice: null },
    { name: 'exactly min', raw: '0.1', value: MIN, notice: null },
    { name: 'exactly max', raw: '10', value: MAX, notice: null },
    { name: 'below min', raw: '0.01', value: MIN, notice: 'min' },
    { name: 'above max', raw: '50', value: MAX, notice: 'max' },
    { name: 'empty', raw: '', value: CURRENT, notice: 'empty' },
    { name: 'whitespace', raw: '  ', value: CURRENT, notice: 'empty' },
    { name: 'exponent', raw: '1e0', value: 1, notice: null },
    { name: 'exponent above max', raw: '1e3', value: MAX, notice: 'max' },
    { name: 'not a number', raw: 'abc', value: CURRENT, notice: 'empty' },
    { name: 'Infinity', raw: 'Infinity', value: CURRENT, notice: 'empty' },
    { name: 'minus Infinity', raw: '-Infinity', value: CURRENT, notice: 'empty' },
    { name: 'number followed by text', raw: '5abc', value: CURRENT, notice: 'empty' },
    { name: 'decimal comma', raw: '1,5', value: CURRENT, notice: 'empty' },
    { name: 'surrounding spaces', raw: ' 5 ', value: 5, notice: null },
    { name: 'hex literal read by Number', raw: '0x5', value: 5, notice: null },
    { name: 'rounds silently to the step grid', raw: '0.7071', value: 0.707, notice: null },
    { name: 'rounds half up to the step grid', raw: '1.2345', value: 1.235, notice: null },
  ];

  it.each(CASES)('$name', ({ raw, value, notice }) => {
    expect(resolveNumericEntry(raw, MIN, MAX, CURRENT, STEP)).toEqual({ value, notice });
  });

  // Step grids of the other numeric parameters: whole hertz from a minimum of 20 Hz, and
  // tenths of a decibel from -30 dB
  it.each([
    { raw: '1000.5', min: 20, max: 20000, step: 1, value: 1001 },
    { raw: '1000.4', min: 20, max: 20000, step: 1, value: 1000 },
    { raw: '1.26', min: -30, max: 30, step: 0.1, value: 1.3 },
    { raw: '-0.04', min: -30, max: 30, step: 0.1, value: 0 },
  ])('rounds $raw to $value on a step of $step from $min', ({ raw, min, max, step, value }) => {
    const result = resolveNumericEntry(raw, min, max, min, step);
    expect(result).toEqual({ value, notice: null });
    expect(Object.is(result.value, -0)).toBe(false);
  });

  it.each([0, -1, NaN, Infinity])('leaves an in-range entry unrounded for a step of %s', step => {
    expect(resolveNumericEntry('1.2345', MIN, MAX, CURRENT, step)).toEqual({
      value: 1.2345,
      notice: null,
    });
  });

  it('normalizes -0 to +0', () => {
    const result = resolveNumericEntry('-0', -1, 1, 0.5, STEP);
    expect(Object.is(result.value, 0)).toBe(true);
    expect(result.notice).toBeNull();
  });
});
