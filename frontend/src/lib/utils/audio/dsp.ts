/**
 * Digital Signal Processing utilities for audio filter calculations.
 *
 * Based on Robert Bristow-Johnson's Audio EQ Cookbook:
 * https://www.w3.org/2011/audio/audio-eq-cookbook.html
 *
 * These functions calculate frequency responses for biquad filters
 * used in the audio equalizer visualization.
 */

// ============================================================================
// Types
// ============================================================================

/** Supported filter types */
export type FilterType =
  | 'LowPass'
  | 'HighPass'
  | 'BandPass'
  | 'BandStop'
  | 'BandReject'
  | 'Notch'
  | 'LowShelf'
  | 'HighShelf'
  | 'Peaking'
  | 'AllPass';

/** Base filter configuration */
interface BaseFilter {
  type: FilterType;
  frequency: number;
  passes?: number;
}

/** LowPass/HighPass filter with Q factor */
interface QBasedFilter extends BaseFilter {
  type: 'LowPass' | 'HighPass' | 'AllPass';
  q?: number;
}

/** BandPass/BandReject filter with bandwidth */
interface BandFilter extends BaseFilter {
  type: 'BandPass' | 'BandStop' | 'BandReject' | 'Notch';
  width?: number; // Bandwidth in Hz
  q?: number; // Alternative to width
}

/** Shelf/Peaking filter with gain */
interface GainFilter extends BaseFilter {
  type: 'LowShelf' | 'HighShelf' | 'Peaking';
  q?: number;
  width?: number;
  gain?: number;
}

/** Union type for all filter configurations */
export type FilterConfig = QBasedFilter | BandFilter | GainFilter;

/** Biquad filter coefficients */
export interface BiquadCoefficients {
  b0: number;
  b1: number;
  b2: number;
  a0: number;
  a1: number;
  a2: number;
}

/** Normalized biquad coefficients (divided by a0) */
export interface NormalizedCoefficients {
  b0: number;
  b1: number;
  b2: number;
  a1: number;
  a2: number;
}

// ============================================================================
// Constants
// ============================================================================

/** Default sample rate (matches backend) */
export const DEFAULT_SAMPLE_RATE = 22050;

/** Butterworth Q factor for maximally flat response */
export const BUTTERWORTH_Q = 0.7071067811865476; // 1/sqrt(2)

/** Minimum dB value for clamping */
export const MIN_DB = -96;

/** Maximum dB value for clamping */
export const MAX_DB = 12;

// ============================================================================
// Coefficient Calculation
// ============================================================================

/**
 * Calculate biquad filter coefficients for a given filter configuration.
 *
 * @param filter - Filter configuration
 * @param sampleRate - Sample rate in Hz (default: 48000)
 * @returns Normalized biquad coefficients
 */
export function calculateCoefficients(
  filter: FilterConfig,
  sampleRate: number = DEFAULT_SAMPLE_RATE
): NormalizedCoefficients {
  const fc = filter.frequency;
  const omega = (2 * Math.PI * fc) / sampleRate;
  const sinOmega = Math.sin(omega);
  const cosOmega = Math.cos(omega);

  // Calculate alpha based on filter type
  let alpha: number;
  let q: number;

  if (filter.type === 'BandReject' || filter.type === 'BandStop' || filter.type === 'Notch') {
    // For band-reject filters, convert bandwidth (Hz) to Q
    // Q = center_frequency / bandwidth_hz
    const width = (filter as BandFilter).width ?? 100;
    q = fc / Math.max(1, width);
    q = Math.max(0.1, Math.min(100, q)); // Clamp Q to reasonable range
    alpha = sinOmega / (2 * q);
  } else if (filter.type === 'BandPass') {
    const width = (filter as BandFilter).width ?? 100;
    q = fc / Math.max(1, width);
    q = Math.max(0.1, Math.min(100, q));
    alpha = sinOmega / (2 * q);
  } else if (filter.type === 'LowPass' || filter.type === 'HighPass') {
    // Always use Butterworth Q for LP/HP filters
    q = BUTTERWORTH_Q;
    alpha = sinOmega / (2 * q);
  } else {
    // Other filters use specified Q or default
    q = (filter as QBasedFilter).q ?? BUTTERWORTH_Q;
    q = Math.max(0.1, Math.min(10, q));
    alpha = sinOmega / (2 * q);
  }

  let b0 = 0,
    b1 = 0,
    b2 = 0;
  let a0 = 1,
    a1 = 0,
    a2 = 0;

  switch (filter.type) {
    case 'LowPass':
      b0 = (1 - cosOmega) / 2;
      b1 = 1 - cosOmega;
      b2 = (1 - cosOmega) / 2;
      a0 = 1 + alpha;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha;
      break;

    case 'HighPass':
      b0 = (1 + cosOmega) / 2;
      b1 = -(1 + cosOmega);
      b2 = (1 + cosOmega) / 2;
      a0 = 1 + alpha;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha;
      break;

    case 'BandPass':
      b0 = alpha;
      b1 = 0;
      b2 = -alpha;
      a0 = 1 + alpha;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha;
      break;

    case 'BandStop':
    case 'BandReject':
    case 'Notch':
      b0 = 1;
      b1 = -2 * cosOmega;
      b2 = 1;
      a0 = 1 + alpha;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha;
      break;

    case 'AllPass':
      b0 = 1 - alpha;
      b1 = -2 * cosOmega;
      b2 = 1 + alpha;
      a0 = 1 + alpha;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha;
      break;

    case 'LowShelf': {
      const gain = (filter as GainFilter).gain ?? 0;
      const A = Math.pow(10, gain / 40);
      const beta = Math.sqrt(A) / q;
      b0 = A * (A + 1 - (A - 1) * cosOmega + beta * sinOmega);
      b1 = 2 * A * (A - 1 - (A + 1) * cosOmega);
      b2 = A * (A + 1 - (A - 1) * cosOmega - beta * sinOmega);
      a0 = A + 1 + (A - 1) * cosOmega + beta * sinOmega;
      a1 = -2 * (A - 1 + (A + 1) * cosOmega);
      a2 = A + 1 + (A - 1) * cosOmega - beta * sinOmega;
      break;
    }

    case 'HighShelf': {
      const gain = (filter as GainFilter).gain ?? 0;
      const A = Math.pow(10, gain / 40);
      const beta = Math.sqrt(A) / q;
      b0 = A * (A + 1 + (A - 1) * cosOmega + beta * sinOmega);
      b1 = -2 * A * (A - 1 + (A + 1) * cosOmega);
      b2 = A * (A + 1 + (A - 1) * cosOmega - beta * sinOmega);
      a0 = A + 1 - (A - 1) * cosOmega + beta * sinOmega;
      a1 = 2 * (A - 1 - (A + 1) * cosOmega);
      a2 = A + 1 - (A - 1) * cosOmega - beta * sinOmega;
      break;
    }

    case 'Peaking': {
      const gain = (filter as GainFilter).gain ?? 0;
      const A = Math.pow(10, gain / 40);
      b0 = 1 + alpha * A;
      b1 = -2 * cosOmega;
      b2 = 1 - alpha * A;
      a0 = 1 + alpha / A;
      a1 = -2 * cosOmega;
      a2 = 1 - alpha / A;
      break;
    }

    default:
      // Unknown filter type - return unity (no effect)
      return { b0: 1, b1: 0, b2: 0, a1: 0, a2: 0 };
  }

  // Normalize coefficients by a0
  return {
    b0: b0 / a0,
    b1: b1 / a0,
    b2: b2 / a0,
    a1: a1 / a0,
    a2: a2 / a0,
  };
}

// ============================================================================
// Frequency Response Calculation
// ============================================================================

/**
 * Calculate the magnitude response of a filter at a given frequency.
 *
 * @param coeffs - Normalized biquad coefficients
 * @param frequency - Frequency to evaluate (Hz)
 * @param sampleRate - Sample rate (Hz)
 * @returns Magnitude (linear, not dB)
 */
export function calculateMagnitudeResponse(
  coeffs: NormalizedCoefficients,
  frequency: number,
  sampleRate: number = DEFAULT_SAMPLE_RATE
): number {
  const w = (2 * Math.PI * frequency) / sampleRate;

  const cosW = Math.cos(w);
  const sinW = Math.sin(w);
  const cos2W = 2 * cosW * cosW - 1; // cos(2w) identity
  const sin2W = 2 * sinW * cosW; // sin(2w) identity

  // Complex numerator: b0 + b1*e^-jw + b2*e^-j2w
  const numReal = coeffs.b0 + coeffs.b1 * cosW + coeffs.b2 * cos2W;
  const numImag = -coeffs.b1 * sinW - coeffs.b2 * sin2W;

  // Complex denominator: 1 + a1*e^-jw + a2*e^-j2w
  const denReal = 1 + coeffs.a1 * cosW + coeffs.a2 * cos2W;
  const denImag = -coeffs.a1 * sinW - coeffs.a2 * sin2W;

  // Magnitude = |numerator| / |denominator|
  const numMag = Math.hypot(numReal, numImag);
  const denMag = Math.hypot(denReal, denImag);

  return numMag / Math.max(1e-10, denMag);
}

/**
 * Calculate the frequency response of a single filter in dB.
 *
 * @param filter - Filter configuration
 * @param frequency - Frequency to evaluate (Hz)
 * @param sampleRate - Sample rate (Hz)
 * @returns Gain in dB
 */
export function calculateFilterResponse(
  filter: FilterConfig,
  frequency: number,
  sampleRate: number = DEFAULT_SAMPLE_RATE
): number {
  const passes = filter.passes ?? 0;

  // If no passes (0dB attenuation), return flat response
  if (passes === 0) {
    return 0;
  }

  const coeffs = calculateCoefficients(filter, sampleRate);
  let magnitude = calculateMagnitudeResponse(coeffs, frequency, sampleRate);

  // For high-pass filters, ensure response doesn't exceed unity at high frequencies
  if (filter.type === 'HighPass') {
    const freqRatio = frequency / filter.frequency;
    if (freqRatio > 10) {
      magnitude = Math.min(magnitude, 1.0);
    }
  }

  // Apply cascaded filter response for multiple passes
  const cascadedMagnitude = Math.pow(magnitude, passes);

  // Convert to dB
  const db = 20 * Math.log10(Math.max(1e-10, cascadedMagnitude));

  return Math.max(MIN_DB, Math.min(MAX_DB, db));
}

/**
 * Calculate the combined frequency response of multiple filters in dB.
 *
 * @param filters - Array of filter configurations
 * @param frequency - Frequency to evaluate (Hz)
 * @param sampleRate - Sample rate (Hz)
 * @returns Combined gain in dB
 */
export function calculateCombinedResponse(
  filters: FilterConfig[],
  frequency: number,
  sampleRate: number = DEFAULT_SAMPLE_RATE
): number {
  let totalGain = 0;

  for (const filter of filters) {
    const filterGain = calculateFilterResponse(filter, frequency, sampleRate);

    // Skip if calculation failed
    if (!isFinite(filterGain)) {
      continue;
    }

    totalGain += filterGain;
  }

  // Return flat response if calculation fails
  if (!isFinite(totalGain)) {
    return 0;
  }

  return Math.max(MIN_DB, Math.min(MAX_DB, totalGain));
}

// ============================================================================
// Frequency Response Curve Generation
// ============================================================================

/** Point on a frequency response curve */
export interface ResponsePoint {
  frequency: number;
  gain: number;
}

/**
 * Generate a frequency response curve for visualization.
 *
 * @param filters - Array of filter configurations
 * @param minFreq - Minimum frequency (Hz)
 * @param maxFreq - Maximum frequency (Hz)
 * @param numPoints - Number of points to generate
 * @param sampleRate - Sample rate (Hz)
 * @returns Array of frequency/gain points
 */
export function generateResponseCurve(
  filters: FilterConfig[],
  minFreq: number = 20,
  maxFreq: number = 20000,
  numPoints: number = 200,
  sampleRate: number = DEFAULT_SAMPLE_RATE
): ResponsePoint[] {
  const points: ResponsePoint[] = [];

  // Use logarithmic spacing for frequency points
  const logMin = Math.log10(minFreq);
  const logMax = Math.log10(maxFreq);
  const logStep = (logMax - logMin) / (numPoints - 1);

  for (let i = 0; i < numPoints; i++) {
    const frequency = Math.pow(10, logMin + i * logStep);
    const gain = calculateCombinedResponse(filters, frequency, sampleRate);
    points.push({ frequency, gain });
  }

  return points;
}

// ============================================================================
// Utility Functions
// ============================================================================

/**
 * Convert bandwidth in Hz to Q factor.
 *
 * @param centerFreq - Center frequency (Hz)
 * @param bandwidthHz - Bandwidth (Hz)
 * @returns Q factor
 */
export function bandwidthToQ(centerFreq: number, bandwidthHz: number): number {
  return centerFreq / Math.max(1, bandwidthHz);
}

/**
 * Convert Q factor to bandwidth in Hz.
 *
 * @param centerFreq - Center frequency (Hz)
 * @param q - Q factor
 * @returns Bandwidth (Hz)
 */
export function qToBandwidth(centerFreq: number, q: number): number {
  return centerFreq / Math.max(0.1, q);
}

/**
 * Check if a filter type uses width parameter instead of Q.
 */
export function usesWidthParameter(type: FilterType): boolean {
  return type === 'BandPass' || type === 'BandReject' || type === 'BandStop' || type === 'Notch';
}

/**
 * Check if a filter type uses gain parameter.
 */
export function usesGainParameter(type: FilterType): boolean {
  return type === 'LowShelf' || type === 'HighShelf' || type === 'Peaking';
}
