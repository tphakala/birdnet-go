/**
 * Tests for Digital Signal Processing utilities.
 *
 * These tests verify the biquad filter coefficient calculations
 * and frequency response computations based on the Audio EQ Cookbook.
 */
import { describe, it, expect } from 'vitest';
import {
  calculateCoefficients,
  calculateMagnitudeResponse,
  calculateFilterResponse,
  calculateCombinedResponse,
  generateResponseCurve,
  bandwidthToQ,
  qToBandwidth,
  usesWidthParameter,
  usesGainParameter,
  DEFAULT_SAMPLE_RATE,
  BUTTERWORTH_Q,
  MIN_DB,
  MAX_DB,
  type FilterConfig,
} from './dsp';

describe('DSP Utilities', () => {
  describe('Constants', () => {
    it('should have correct default sample rate', () => {
      expect(DEFAULT_SAMPLE_RATE).toBe(48000);
    });

    it('should have correct Butterworth Q factor', () => {
      // 1/sqrt(2) = 0.7071067811865476
      expect(BUTTERWORTH_Q).toBeCloseTo(0.707, 3);
    });

    it('should have valid dB range', () => {
      expect(MIN_DB).toBe(-96);
      expect(MAX_DB).toBe(12);
    });
  });

  describe('calculateCoefficients', () => {
    describe('LowPass filter', () => {
      it('should calculate valid coefficients for LowPass', () => {
        const filter: FilterConfig = {
          type: 'LowPass',
          frequency: 1000,
        };

        const coeffs = calculateCoefficients(filter);

        // All coefficients should be finite numbers
        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.b1)).toBe(true);
        expect(Number.isFinite(coeffs.b2)).toBe(true);
        expect(Number.isFinite(coeffs.a1)).toBe(true);
        expect(Number.isFinite(coeffs.a2)).toBe(true);

        // For lowpass: b0 and b2 should be equal (symmetric numerator)
        expect(coeffs.b0).toBeCloseTo(coeffs.b2, 10);
      });

      it('should use Butterworth Q for LowPass', () => {
        const filter: FilterConfig = {
          type: 'LowPass',
          frequency: 1000,
          q: 5, // This should be ignored for LowPass
        };

        const coeffs = calculateCoefficients(filter);
        const coeffsNoQ = calculateCoefficients({
          type: 'LowPass',
          frequency: 1000,
        });

        // Coefficients should be identical regardless of Q parameter
        expect(coeffs.b0).toBeCloseTo(coeffsNoQ.b0, 10);
        expect(coeffs.a1).toBeCloseTo(coeffsNoQ.a1, 10);
      });
    });

    describe('HighPass filter', () => {
      it('should calculate valid coefficients for HighPass', () => {
        const filter: FilterConfig = {
          type: 'HighPass',
          frequency: 1000,
        };

        const coeffs = calculateCoefficients(filter);

        // All coefficients should be finite numbers
        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.b1)).toBe(true);
        expect(Number.isFinite(coeffs.b2)).toBe(true);

        // For highpass: b0 and b2 should be equal, b1 should be negative
        expect(coeffs.b0).toBeCloseTo(coeffs.b2, 10);
        expect(coeffs.b1).toBeLessThan(0);
      });
    });

    describe('BandReject (Notch) filter', () => {
      it('should calculate valid coefficients for BandReject', () => {
        const filter: FilterConfig = {
          type: 'BandReject',
          frequency: 1000,
          width: 100,
        };

        const coeffs = calculateCoefficients(filter);

        // All coefficients should be finite numbers
        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.b1)).toBe(true);
        expect(Number.isFinite(coeffs.b2)).toBe(true);

        // For notch: b0 and b2 should be 1 (normalized by a0)
        // The characteristic of a notch filter
        expect(coeffs.b0).toBeCloseTo(coeffs.b2, 10);
      });

      it('should handle very narrow bandwidth (high Q)', () => {
        const filter: FilterConfig = {
          type: 'BandReject',
          frequency: 1000,
          width: 10, // Very narrow - 10Hz width at 1kHz = Q of 100
        };

        const coeffs = calculateCoefficients(filter);

        // Should still produce finite coefficients
        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.b1)).toBe(true);
        expect(Number.isFinite(coeffs.b2)).toBe(true);
        expect(Number.isFinite(coeffs.a1)).toBe(true);
        expect(Number.isFinite(coeffs.a2)).toBe(true);
      });

      it('should handle wide bandwidth (low Q)', () => {
        const filter: FilterConfig = {
          type: 'BandReject',
          frequency: 1000,
          width: 500, // Wide - Q of 2
        };

        const coeffs = calculateCoefficients(filter);

        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.a1)).toBe(true);
      });
    });

    describe('Frequencies near Nyquist', () => {
      it('should handle frequencies close to Nyquist limit', () => {
        const nyquist = DEFAULT_SAMPLE_RATE / 2; // 24000 Hz

        // Test at 90% of Nyquist
        const filter: FilterConfig = {
          type: 'LowPass',
          frequency: nyquist * 0.9, // 21600 Hz
        };

        const coeffs = calculateCoefficients(filter);

        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.a1)).toBe(true);
        expect(Number.isFinite(coeffs.a2)).toBe(true);
      });

      it('should handle frequencies at edge of audible range', () => {
        const filter: FilterConfig = {
          type: 'HighPass',
          frequency: 20, // Lowest audible frequency
        };

        const coeffs = calculateCoefficients(filter);

        expect(Number.isFinite(coeffs.b0)).toBe(true);
        expect(Number.isFinite(coeffs.a1)).toBe(true);
      });
    });

    describe('Unknown filter type', () => {
      it('should return unity coefficients for unknown type', () => {
        const filter = {
          type: 'Unknown' as FilterConfig['type'],
          frequency: 1000,
        };

        const coeffs = calculateCoefficients(filter);

        // Unity filter: passes signal unchanged
        expect(coeffs.b0).toBe(1);
        expect(coeffs.b1).toBe(0);
        expect(coeffs.b2).toBe(0);
        expect(coeffs.a1).toBe(0);
        expect(coeffs.a2).toBe(0);
      });
    });
  });

  describe('calculateMagnitudeResponse', () => {
    it('should return unity at DC for LowPass', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 1000,
      };
      const coeffs = calculateCoefficients(filter);

      // At DC (0 Hz), lowpass should pass signal fully
      const magnitude = calculateMagnitudeResponse(coeffs, 0.001);
      expect(magnitude).toBeCloseTo(1, 1);
    });

    it('should return unity at high frequencies for HighPass', () => {
      const filter: FilterConfig = {
        type: 'HighPass',
        frequency: 1000,
      };
      const coeffs = calculateCoefficients(filter);

      // At high frequencies, highpass should pass signal
      const magnitude = calculateMagnitudeResponse(coeffs, 10000);
      expect(magnitude).toBeCloseTo(1, 1);
    });

    it('should return ~0.707 (-3dB) at cutoff for Butterworth', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 1000,
      };
      const coeffs = calculateCoefficients(filter);

      // At cutoff frequency, Butterworth filter should have -3dB (magnitude ~0.707)
      const magnitude = calculateMagnitudeResponse(coeffs, 1000);
      expect(magnitude).toBeCloseTo(BUTTERWORTH_Q, 1);
    });
  });

  describe('calculateFilterResponse', () => {
    it('should return 0dB when passes is 0', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 1000,
        passes: 0,
      };

      const response = calculateFilterResponse(filter, 5000);
      expect(response).toBe(0);
    });

    it('should return ~-3dB at cutoff for single pass LowPass', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 1000,
        passes: 1,
      };

      const response = calculateFilterResponse(filter, 1000);
      // -3dB is approximately -3.01
      expect(response).toBeCloseTo(-3, 0);
    });

    it('should return ~-6dB at cutoff for two passes', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 1000,
        passes: 2,
      };

      const response = calculateFilterResponse(filter, 1000);
      // Two passes doubles the dB attenuation
      expect(response).toBeCloseTo(-6, 0);
    });

    it('should clamp response to MIN_DB', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 100,
        passes: 4,
      };

      // At high frequency with many passes, response should hit MIN_DB
      const response = calculateFilterResponse(filter, 20000);
      expect(response).toBeGreaterThanOrEqual(MIN_DB);
    });

    it('should attenuate deeply at notch center frequency', () => {
      const filter: FilterConfig = {
        type: 'BandReject',
        frequency: 1000,
        width: 100,
        passes: 1,
      };

      const response = calculateFilterResponse(filter, 1000);
      // Notch with Q=10 (1000Hz center / 100Hz width) should have significant attenuation
      // Expect at least -20dB for a reasonably narrow notch at center frequency
      expect(response).toBeLessThan(-20);
    });

    describe('Multiple cascaded passes', () => {
      it('should increase attenuation with more passes for LowPass', () => {
        const filter1: FilterConfig = { type: 'LowPass', frequency: 1000, passes: 1 };
        const filter2: FilterConfig = { type: 'LowPass', frequency: 1000, passes: 2 };
        const filter4: FilterConfig = { type: 'LowPass', frequency: 1000, passes: 4 };

        const testFreq = 2000; // One octave above cutoff
        const response1 = calculateFilterResponse(filter1, testFreq);
        const response2 = calculateFilterResponse(filter2, testFreq);
        const response4 = calculateFilterResponse(filter4, testFreq);

        // Each pass should add ~12dB/octave for Butterworth
        expect(response2).toBeLessThan(response1);
        expect(response4).toBeLessThan(response2);

        // Approximately double the dB for each doubling of passes
        expect(response2).toBeCloseTo(response1 * 2, 0);
        expect(response4).toBeCloseTo(response1 * 4, 0);
      });

      it('should increase attenuation with more passes for HighPass', () => {
        const filter1: FilterConfig = { type: 'HighPass', frequency: 1000, passes: 1 };
        const filter2: FilterConfig = { type: 'HighPass', frequency: 1000, passes: 2 };

        const testFreq = 500; // One octave below cutoff
        const response1 = calculateFilterResponse(filter1, testFreq);
        const response2 = calculateFilterResponse(filter2, testFreq);

        expect(response2).toBeLessThan(response1);
      });

      it('should increase notch depth with more passes', () => {
        const filter1: FilterConfig = {
          type: 'BandReject',
          frequency: 1000,
          width: 200, // Wider notch so response doesn't hit MIN_DB floor
          passes: 1,
        };
        const filter2: FilterConfig = {
          type: 'BandReject',
          frequency: 1000,
          width: 200,
          passes: 2,
        };

        // Test slightly off-center where response isn't clamped to MIN_DB
        const response1 = calculateFilterResponse(filter1, 1050);
        const response2 = calculateFilterResponse(filter2, 1050);

        // More passes should deepen the notch (or both hit MIN_DB floor)
        expect(response2).toBeLessThanOrEqual(response1);
        // At least one should show attenuation
        expect(response1).toBeLessThan(0);
      });
    });
  });

  describe('calculateCombinedResponse', () => {
    it('should return flat response for empty filter array', () => {
      const response = calculateCombinedResponse([], 1000);
      expect(response).toBe(0);
    });

    it('should sum filter responses in dB', () => {
      const filters: FilterConfig[] = [
        { type: 'LowPass', frequency: 5000, passes: 1 },
        { type: 'HighPass', frequency: 100, passes: 1 },
      ];

      // At 1kHz (within passband of both), should be near 0dB
      const response = calculateCombinedResponse(filters, 1000);
      expect(response).toBeCloseTo(0, 0);
    });

    it('should handle multiple filters with different types', () => {
      const filters: FilterConfig[] = [
        { type: 'HighPass', frequency: 200, passes: 1 },
        { type: 'LowPass', frequency: 8000, passes: 1 },
        { type: 'BandReject', frequency: 1000, width: 50, passes: 1 },
      ];

      // Should produce valid combined response
      const response = calculateCombinedResponse(filters, 500);
      expect(Number.isFinite(response)).toBe(true);
      expect(response).toBeGreaterThanOrEqual(MIN_DB);
      expect(response).toBeLessThanOrEqual(MAX_DB);
    });

    it('should skip filters with invalid response', () => {
      // This tests the isFinite check in the function
      const filters: FilterConfig[] = [{ type: 'LowPass', frequency: 1000, passes: 1 }];

      const response = calculateCombinedResponse(filters, 1000);
      expect(Number.isFinite(response)).toBe(true);
    });
  });

  describe('generateResponseCurve', () => {
    it('should generate correct number of points', () => {
      const filters: FilterConfig[] = [{ type: 'LowPass', frequency: 1000, passes: 1 }];

      const curve = generateResponseCurve(filters, 20, 20000, 100);
      expect(curve.length).toBe(100);
    });

    it('should generate points with logarithmic spacing', () => {
      const filters: FilterConfig[] = [];
      const curve = generateResponseCurve(filters, 20, 20000, 5);

      // First and last frequencies should match bounds
      expect(curve[0].frequency).toBeCloseTo(20, 0);
      expect(curve[4].frequency).toBeCloseTo(20000, 0);

      // Points should be logarithmically spaced
      // log10(20) = 1.301, log10(20000) = 4.301
      // Middle point should be around 10^2.801 ≈ 632 Hz
      expect(curve[2].frequency).toBeCloseTo(632, -1);
    });

    it('should return flat response for no filters', () => {
      const curve = generateResponseCurve([], 20, 20000, 50);

      // All points should have 0dB gain
      curve.forEach(point => {
        expect(point.gain).toBe(0);
      });
    });

    it('should show LowPass rolloff', () => {
      const filters: FilterConfig[] = [{ type: 'LowPass', frequency: 1000, passes: 1 }];

      const curve = generateResponseCurve(filters, 20, 20000, 50);

      // Check response at specific frequencies
      // Low frequency (near 20Hz) should be near 0dB
      const lowFreqResponse = curve[0]; // First point at 20Hz
      expect(lowFreqResponse.gain).toBeCloseTo(0, 0);

      // High frequency should be attenuated - last point at 20kHz
      const highFreqResponse = curve[curve.length - 1];
      expect(highFreqResponse.gain).toBeLessThan(-20);
    });
  });

  describe('Utility functions', () => {
    describe('bandwidthToQ', () => {
      it('should convert bandwidth to Q correctly', () => {
        // Q = center_freq / bandwidth
        expect(bandwidthToQ(1000, 100)).toBe(10);
        expect(bandwidthToQ(1000, 1000)).toBe(1);
        expect(bandwidthToQ(1000, 50)).toBe(20);
      });

      it('should handle edge cases', () => {
        // Should not divide by zero
        expect(bandwidthToQ(1000, 0)).toBe(1000);
        expect(bandwidthToQ(1000, 1)).toBe(1000);
      });
    });

    describe('qToBandwidth', () => {
      it('should convert Q to bandwidth correctly', () => {
        // bandwidth = center_freq / Q
        expect(qToBandwidth(1000, 10)).toBe(100);
        expect(qToBandwidth(1000, 1)).toBe(1000);
        expect(qToBandwidth(1000, 20)).toBe(50);
      });

      it('should clamp Q to minimum', () => {
        // Q is clamped to 0.1 minimum
        expect(qToBandwidth(1000, 0.05)).toBe(10000); // 1000 / 0.1
        expect(qToBandwidth(1000, 0)).toBe(10000);
      });
    });

    describe('usesWidthParameter', () => {
      it('should return true for band filters', () => {
        expect(usesWidthParameter('BandPass')).toBe(true);
        expect(usesWidthParameter('BandReject')).toBe(true);
        expect(usesWidthParameter('BandStop')).toBe(true);
        expect(usesWidthParameter('Notch')).toBe(true);
      });

      it('should return false for other filters', () => {
        expect(usesWidthParameter('LowPass')).toBe(false);
        expect(usesWidthParameter('HighPass')).toBe(false);
        expect(usesWidthParameter('LowShelf')).toBe(false);
        expect(usesWidthParameter('HighShelf')).toBe(false);
        expect(usesWidthParameter('Peaking')).toBe(false);
        expect(usesWidthParameter('AllPass')).toBe(false);
      });
    });

    describe('usesGainParameter', () => {
      it('should return true for shelf and peaking filters', () => {
        expect(usesGainParameter('LowShelf')).toBe(true);
        expect(usesGainParameter('HighShelf')).toBe(true);
        expect(usesGainParameter('Peaking')).toBe(true);
      });

      it('should return false for other filters', () => {
        expect(usesGainParameter('LowPass')).toBe(false);
        expect(usesGainParameter('HighPass')).toBe(false);
        expect(usesGainParameter('BandPass')).toBe(false);
        expect(usesGainParameter('BandReject')).toBe(false);
        expect(usesGainParameter('AllPass')).toBe(false);
      });
    });
  });

  describe('Edge cases and numerical stability', () => {
    it('should handle extremely low frequencies', () => {
      const filter: FilterConfig = {
        type: 'HighPass',
        frequency: 1, // 1 Hz - below audible range
        passes: 1,
      };

      const response = calculateFilterResponse(filter, 20);
      expect(Number.isFinite(response)).toBe(true);
    });

    it('should handle frequencies near Nyquist', () => {
      const filter: FilterConfig = {
        type: 'LowPass',
        frequency: 23000, // Near 24kHz Nyquist for 48kHz sample rate
        passes: 1,
      };

      const response = calculateFilterResponse(filter, 20000);
      expect(Number.isFinite(response)).toBe(true);
    });

    it('should handle very high Q values', () => {
      const filter: FilterConfig = {
        type: 'BandReject',
        frequency: 1000,
        width: 1, // Extremely narrow - Q of 1000
        passes: 1,
      };

      const coeffs = calculateCoefficients(filter);
      // Q is clamped to 100 max, so coefficients should still be stable
      expect(Number.isFinite(coeffs.b0)).toBe(true);
      expect(Number.isFinite(coeffs.a1)).toBe(true);
      expect(Number.isFinite(coeffs.a2)).toBe(true);
    });

    it('should produce stable response across frequency range', () => {
      const filter: FilterConfig = {
        type: 'BandReject',
        frequency: 5000,
        width: 200,
        passes: 3,
      };

      // Test at multiple frequencies
      const testFreqs = [20, 100, 500, 1000, 2000, 5000, 8000, 12000, 18000];
      testFreqs.forEach(freq => {
        const response = calculateFilterResponse(filter, freq);
        expect(Number.isFinite(response)).toBe(true);
        expect(response).toBeGreaterThanOrEqual(MIN_DB);
        expect(response).toBeLessThanOrEqual(MAX_DB);
      });
    });
  });
});
