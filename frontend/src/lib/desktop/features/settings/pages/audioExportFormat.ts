/**
 * Audio export format helpers. Kept separate from AudioSettingsPage so the
 * lossless-to-lossy transition logic can be unit-tested without mounting
 * the Svelte component.
 */
import { formatBitrate, getBitrateConfig } from '$lib/utils/audioValidation';

export type ExportFormat = 'wav' | 'mp3' | 'flac' | 'aac' | 'opus';

const EXPORT_FORMATS: readonly ExportFormat[] = ['wav', 'mp3', 'flac', 'aac', 'opus'];

export function isExportFormat(value: unknown): value is ExportFormat {
  return typeof value === 'string' && (EXPORT_FORMATS as readonly string[]).includes(value);
}

// Parse "128k" / "128K" / "128" into a positive number, or null if the
// input cannot be interpreted as a positive numeric bitrate. Returning
// null lets chooseBitrateForFormat distinguish "invalid" from "valid 128k"
// so it can snap to the target format default rather than accept stray
// values. The shared parseNumericBitrate in audioValidation.ts returns a
// hardcoded default on failure and cannot make that distinction.
function parseBitrateNullable(raw: string): number | null {
  const trimmed = raw.trim();
  if (trimmed === '') {
    return null;
  }
  const stripped = trimmed.endsWith('k') || trimmed.endsWith('K') ? trimmed.slice(0, -1) : trimmed;
  const parsed = Number(stripped);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

/**
 * Pick a bitrate string appropriate for the given target format.
 *
 * - For lossless formats (wav, flac): returns '' (bitrate is ignored by the
 *   backend).
 * - For lossy formats (mp3, aac, opus): returns the current bitrate if it
 *   is a valid in-range numeric value for the target format, otherwise
 *   seeds that format's default.
 */
export function chooseBitrateForFormat(target: ExportFormat, current: string): string {
  // getBitrateConfig returns null for lossless formats. That is the single
  // source of truth; calling getExportTypeConfig separately would re-derive
  // the same lossless/lossy split.
  const config = getBitrateConfig(target);
  if (!config) {
    return '';
  }

  const currentValue = parseBitrateNullable(current);
  if (currentValue !== null && currentValue >= config.min && currentValue <= config.max) {
    return formatBitrate(currentValue);
  }

  return formatBitrate(config.default);
}
