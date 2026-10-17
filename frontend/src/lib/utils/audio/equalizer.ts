/**
 * Shared equalizer constants and the commit rule for the equalizer editor's
 * numeric inputs.
 *
 * The defaults mirror the backend filter table (internal/conf/eqfilter_config.go),
 * which the frontend fallback copy eqFilterFallbackConfig.json follows; equalizer.test.ts
 * pins them to that copy.
 */

/** Default Q factor of LowPass and HighPass filters (mirror of conf.DefaultEQQFactor). */
export const DEFAULT_EQ_Q = 0.707;

/**
 * Default bandwidth in Hz: the BandReject Width default, also the width the new-filter
 * form and settings coercion fall back to.
 */
export const DEFAULT_EQ_WIDTH_HZ = 100;

/** Default gain in dB of filters that take a gain. */
export const DEFAULT_EQ_GAIN_DB = 0;

/** Passes value that switches a filter off; a missing passes means off as well. */
export const EQ_PASSES_OFF = 0;

/** Highest passes value the editor offers (the Passes maximum of the filter table). */
export const EQ_OFFERED_MAX_PASSES = 4;

/** Numeric equalizer parameters that the editor shows as number inputs. */
export type EqNumericParameter = 'Frequency' | 'Q' | 'Width' | 'Gain';

/** Step of the frequency and width inputs, in Hz. */
export const EQ_HZ_INPUT_STEP = 1;

/**
 * Step of the Q input. It is fine enough that the default 0.707 sits on the step grid
 * (which starts at the Q minimum) and the arrow keys change Q in small steps; with
 * step 'any', Chromium steps by 1.
 */
export const EQ_Q_INPUT_STEP = 0.001;

/** Step of the gain input, in dB. */
export const EQ_GAIN_INPUT_STEP = 0.1;

/** Step attribute of the input for a numeric parameter. */
export function eqInputStep(name: EqNumericParameter): number {
  switch (name) {
    case 'Frequency':
    case 'Width':
      return EQ_HZ_INPUT_STEP;
    case 'Q':
      return EQ_Q_INPUT_STEP;
    case 'Gain':
      return EQ_GAIN_INPUT_STEP;
  }
}

/**
 * Why a committed entry differs from what the user typed: 'min' and 'max' when it
 * was clamped to that bound, 'empty' when it was not a number and the current value
 * was kept.
 */
export type NumericEntryNotice = 'min' | 'max' | 'empty';

/** Result of resolving a typed entry against a parameter range. */
export interface NumericEntryResult {
  /** The value to show and, unless notice is 'empty', to commit. */
  value: number;
  notice: NumericEntryNotice | null;
}

/**
 * Resolve the text of a numeric input against [min, max]. The text counts as a number
 * only when Number() reads all of it as a finite number (surrounding spaces and hex,
 * binary or octal literals included); blank text, text with other characters such as
 * a decimal comma, NaN and Infinity keep `current`. An entry outside the range is
 * clamped to the nearest bound; -0 becomes 0.
 */
export function resolveNumericEntry(
  raw: string,
  min: number,
  max: number,
  current: number
): NumericEntryResult {
  // Number() reads blank text as 0, so blank text is rejected first
  const parsed = raw.trim() === '' ? NaN : Number(raw);
  if (!Number.isFinite(parsed)) {
    return { value: current, notice: 'empty' };
  }
  // Adding 0 turns -0 into +0 and leaves every other number unchanged
  const value = parsed + 0;
  if (value < min) return { value: min, notice: 'min' };
  if (value > max) return { value: max, notice: 'max' };
  return { value, notice: null };
}
