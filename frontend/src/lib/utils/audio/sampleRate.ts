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
 * On any failure, returns all candidate rates as unverified.
 */
export async function fetchDeviceCapabilities(
  deviceId: string,
  signal?: AbortSignal
): Promise<DeviceCapabilitiesResult> {
  const response = await fetch(
    `/api/v2/system/audio/devices/capabilities?deviceId=${encodeURIComponent(deviceId)}`,
    signal ? { signal } : undefined
  );

  if (!response.ok) {
    return { options: fallbackOptions(), verified: false };
  }

  const responseData: unknown = await response.json();
  if (
    !responseData ||
    typeof responseData !== 'object' ||
    !('sampleRates' in responseData) ||
    !Array.isArray((responseData as Record<string, unknown>).sampleRates)
  ) {
    return { options: fallbackOptions(), verified: false };
  }

  const data = responseData as { sampleRates: number[]; verified: boolean };
  if (data.sampleRates.length === 0) {
    return { options: fallbackOptions(), verified: false };
  }

  return {
    options: data.sampleRates.map(rate => ({
      value: String(rate),
      label: formatSampleRateLabel(rate),
    })),
    verified: data.verified,
  };
}
