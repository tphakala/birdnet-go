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

/** Attenuation in dB that one filter pass adds, for labelling a passes value. */
export const EQ_DB_PER_PASS = 12;

/** Default frequency in Hz of a LowPass filter. */
export const EQ_LOWPASS_FREQUENCY_HZ = 15000;

/** Default frequency in Hz of a HighPass filter. */
export const EQ_HIGHPASS_FREQUENCY_HZ = 100;

/** Default frequency in Hz of BandReject and BandPass filters and of an unknown type. */
export const EQ_GENERIC_FREQUENCY_HZ = 1000;

/**
 * Default frequency in Hz for a filter type, the Frequency default of the filter table
 * (LowPass 15000, HighPass 100, BandReject and BandPass 1000). Used for a stored filter
 * that has no frequency; equalizer.test.ts pins it to the table.
 */
export function defaultEqFrequencyHz(type: string): number {
  switch (type) {
    case 'LowPass':
      return EQ_LOWPASS_FREQUENCY_HZ;
    case 'HighPass':
      return EQ_HIGHPASS_FREQUENCY_HZ;
    default:
      return EQ_GENERIC_FREQUENCY_HZ;
  }
}

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

/** A notice about one committed entry: why it changed, and the value it became. */
export interface EqFieldNotice {
  kind: NumericEntryNotice;
  value: number;
}

/** Result of resolving a typed entry against a parameter range. */
export interface NumericEntryResult {
  /** The value to show and, unless notice is 'empty', to commit. */
  value: number;
  notice: NumericEntryNotice | null;
}

// Decimal places used to cancel floating-point error when counting steps, far below any
// step the editor uses
const STEP_COUNT_PRECISION = 9;

// Number of decimal places in a number's shortest decimal form (2 for 0.25, 7 for 1e-7)
function decimalPlaces(n: number): number {
  const parts = String(n).toLowerCase().split('e');
  const fraction = parts.at(0)?.split('.').at(1) ?? '';
  return Math.max(0, fraction.length - Number(parts.at(1) ?? 0));
}

/**
 * Resolve the text of a numeric input against [min, max] and the input's step grid,
 * which starts at min as it does for an HTML number input. The text counts as a number
 * only when Number() reads all of it as a finite number (surrounding spaces and hex,
 * binary or octal literals included); blank text, text with other characters such as
 * a decimal comma, NaN and Infinity keep `current`. An entry outside the range is
 * clamped to the nearest bound. An entry inside it is rounded to the nearest step
 * without a notice, capped at max; when min and max both lie on the step grid, as every
 * bound of the filter table does, a typed entry never leaves the input off the grid. A
 * step that is not a positive finite number leaves it unrounded. `current` is returned as
 * it is, so a stored value off the grid stays off it until the field is committed.
 * -0 becomes 0.
 */
export function resolveNumericEntry(
  raw: string,
  min: number,
  max: number,
  current: number,
  step: number
): NumericEntryResult {
  // Number() reads blank text as 0, so blank text is rejected first
  const parsed = raw.trim() === '' ? NaN : Number(raw);
  if (!Number.isFinite(parsed)) {
    return { value: current, notice: 'empty' };
  }
  if (parsed < min) return { value: min, notice: 'min' };
  if (parsed > max) return { value: max, notice: 'max' };
  if (!(step > 0) || !Number.isFinite(step)) return { value: parsed + 0, notice: null };
  const steps = Math.round(Number(((parsed - min) / step).toFixed(STEP_COUNT_PRECISION)));
  const places = Math.max(decimalPlaces(step), decimalPlaces(min));
  const rounded = Math.min(max, Number((min + steps * step).toFixed(places)));
  // Adding 0 turns -0 into +0 and leaves every other number unchanged
  return { value: rounded + 0, notice: null };
}
