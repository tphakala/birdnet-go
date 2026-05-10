/**
 * Shared sample rate constants and utilities for device capabilities.
 */

export const CANDIDATE_SAMPLE_RATES = [48000, 96000, 192000, 256000, 384000] as const;

export type SampleRateOption = { value: string; label: string };

export interface DeviceCapabilitiesResult {
  options: SampleRateOption[];
  verified: boolean;
}

export function formatSampleRateLabel(rate: number): string {
  return rate >= 1000 ? `${rate / 1000} kHz` : `${rate} Hz`;
}

function fallbackOptions(): SampleRateOption[] {
  return CANDIDATE_SAMPLE_RATES.map(rate => ({
    value: String(rate),
    label: `${rate / 1000} kHz`,
  }));
}

/**
 * Fetch device sample rate capabilities from the backend API.
 * Returns supported rates with verified status.
 * On any failure (network, malformed JSON, invalid data), returns all
 * candidate rates as unverified. Only AbortError is re-thrown so callers
 * can detect request cancellation.
 */
export async function fetchDeviceCapabilities(
  deviceId: string,
  signal?: AbortSignal
): Promise<DeviceCapabilitiesResult> {
  try {
    const response = await fetch(
      `/api/v2/system/audio/devices/capabilities?deviceId=${encodeURIComponent(deviceId)}`,
      signal ? { signal } : undefined
    );

    if (!response.ok) {
      return { options: fallbackOptions(), verified: false };
    }

    let responseData: unknown;
    try {
      responseData = await response.json();
    } catch {
      return { options: fallbackOptions(), verified: false };
    }

    if (
      !responseData ||
      typeof responseData !== 'object' ||
      !('sampleRates' in responseData) ||
      !Array.isArray((responseData as Record<string, unknown>).sampleRates)
    ) {
      return { options: fallbackOptions(), verified: false };
    }

    const raw = responseData as { sampleRates: unknown[]; verified?: unknown };
    const rates = raw.sampleRates.filter(
      (r): r is number => typeof r === 'number' && Number.isFinite(r)
    );

    if (rates.length === 0) {
      return { options: fallbackOptions(), verified: false };
    }

    return {
      options: rates.map(rate => ({
        value: String(rate),
        label: formatSampleRateLabel(rate),
      })),
      verified: typeof raw.verified === 'boolean' ? raw.verified : false,
    };
  } catch (error: unknown) {
    if (error instanceof Error && error.name === 'AbortError') throw error;
    return { options: fallbackOptions(), verified: false };
  }
}
