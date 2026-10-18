/**
 * Frequency axis for bat spectrograms.
 *
 * Bat spectrograms are rendered at the clip's own capture rate, so the image
 * spans 0 Hz to half that rate (the Nyquist frequency). The axis labels are
 * derived from the clip sample rate the API reports; without a known rate there
 * is no axis, because guessing a range would mislabel the image.
 */

/** Hertz in one kilohertz. */
export const HZ_PER_KHZ = 1000;

/** The highest frequency a sampled signal holds is its rate divided by this. */
export const NYQUIST_DIVISOR = 2;

/** Tick spacings (kHz) tried in order, from the finest to the coarsest. */
const BAT_TICK_STEPS_KHZ = [10, 20, 40, 50, 100] as const;

/** Most tick labels drawn on one axis. */
export const BAT_MAX_TICKS = 6;

/**
 * The top tick sits at most this fraction of the Nyquist frequency up the
 * axis, so its label never touches the top edge of the image.
 */
export const BAT_TOP_TICK_MAX_FRACTION = 0.95;

/** Tolerance for floating point error when counting the ticks that fit. */
const TICK_FIT_EPSILON = 1e-9;

/** A linear frequency axis from 0 to `nyquistKHz`. */
export interface FrequencyAxis {
  /** Top of the axis in kHz (half the clip sample rate). */
  nyquistKHz: number;
  /** Tick positions in kHz, highest first. Empty when no step fits the range. */
  ticksKHz: number[];
}

function ticksForStep(stepKHz: number, maxTickKHz: number): number[] {
  const count = Math.floor(maxTickKHz / stepKHz + TICK_FIT_EPSILON);
  return Array.from({ length: count }, (_, i) => (count - i) * stepKHz);
}

/**
 * Builds the frequency axis for a bat spectrogram rendered at `sampleRateHz`.
 *
 * Returns null when the rate is unknown (missing, zero, negative or not
 * finite). Otherwise the ticks are the multiples of the first step in
 * BAT_TICK_STEPS_KHZ that yields at most BAT_MAX_TICKS labels at or below
 * BAT_TOP_TICK_MAX_FRACTION of the Nyquist frequency, falling back to the
 * coarsest step.
 */
export function batFrequencyAxis(sampleRateHz: number | null | undefined): FrequencyAxis | null {
  if (typeof sampleRateHz !== 'number' || !Number.isFinite(sampleRateHz) || sampleRateHz <= 0) {
    return null;
  }

  const nyquistKHz = sampleRateHz / (HZ_PER_KHZ * NYQUIST_DIVISOR);
  const maxTickKHz = nyquistKHz * BAT_TOP_TICK_MAX_FRACTION;

  let ticksKHz: number[] = [];
  for (const step of BAT_TICK_STEPS_KHZ) {
    ticksKHz = ticksForStep(step, maxTickKHz);
    if (ticksKHz.length <= BAT_MAX_TICKS) break;
  }

  return { nyquistKHz, ticksKHz };
}
