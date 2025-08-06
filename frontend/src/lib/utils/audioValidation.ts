/**
 * Audio Validation Utilities
 *
 * Purpose: Provides audio format validation, bitrate configuration,
 * and disk usage formatting utilities used by both components and tests.
 *
 * Features:
 * - Bitrate validation for different audio formats
 * - Bitrate configuration with min/max/step values
 * - Disk usage percentage formatting
 * - Export format type detection
 */

export interface BitrateConfig {
  min: number;
  max: number;
  step: number;
  default: number;
}

export interface ExportTypeConfig {
  requiresBitrate: boolean;
  isLossless: boolean;
  bitrateRange: { min: number; max: number } | null;
}

/**
 * Get bitrate configuration for a given audio format
 * @param format Audio format (mp3, aac, opus, wav, flac)
 * @returns Bitrate configuration or null for lossless formats
 */
export function getBitrateConfig(format: string): BitrateConfig | null {
  const configs: Record<string, BitrateConfig> = {
    mp3: { min: 32, max: 320, step: 32, default: 128 },
    aac: { min: 32, max: 320, step: 32, default: 96 },
    opus: { min: 32, max: 256, step: 32, default: 96 }, // Opus typically maxes at 256k
  };

  if (format in configs) {
    return configs[format as keyof typeof configs];
  }
  return null;
}

/**
 * Validate if a bitrate is within valid range for a given format
 * @param bitrate Bitrate value to validate
 * @param format Audio format
 * @returns true if bitrate is valid, false otherwise
 */
export function validateBitrate(bitrate: number, format: string): boolean {
  if (['aac', 'opus', 'mp3'].includes(format)) {
    return bitrate >= 32 && bitrate <= (format === 'opus' ? 256 : 320);
  }
  return true; // No bitrate validation for lossless formats
}

/**
 * Get export type configuration
 * @param type Audio export type
 * @returns Configuration object with bitrate requirements and ranges
 */
export function getExportTypeConfig(type: string): ExportTypeConfig {
  const requiresBitrate = ['aac', 'opus', 'mp3'].includes(type);
  const isLossless = ['wav', 'flac'].includes(type);

  return {
    requiresBitrate,
    isLossless,
    bitrateRange: requiresBitrate ? { min: 32, max: type === 'opus' ? 256 : 320 } : null,
  };
}

/**
 * Format bitrate value to include 'k' suffix
 * @param bitrate Bitrate as number or string
 * @returns Formatted bitrate string with 'k' suffix
 */
export function formatBitrate(bitrate: number | string): string {
  return typeof bitrate === 'number'
    ? `${bitrate}k`
    : bitrate.endsWith('k')
      ? bitrate
      : `${bitrate}k`;
}

/**
 * Parse numeric bitrate from string format (e.g., "96k" -> 96)
 * @param bitrate Bitrate string with or without 'k' suffix
 * @returns Numeric bitrate value, defaults to 96 if parsing fails
 */
export function parseNumericBitrate(bitrate: string): number {
  if (!bitrate) return 96;
  const parsed = parseInt(bitrate.replace('k', ''));
  return isNaN(parsed) ? 96 : parsed;
}

/**
 * Format disk usage percentage to ensure it has '%' suffix
 * @param value Usage value as string
 * @returns Formatted usage string with '%' suffix
 */
export function formatDiskUsage(value: string): string {
  // Remove any non-numeric characters except %
  const cleaned = value.replace(/[^0-9%]/g, '');

  // Normalize multiple % signs to single %
  const normalized = cleaned.replace(/%+/g, '%');

  // If it already has %, return as is
  if (normalized.endsWith('%')) {
    return normalized;
  }

  // Otherwise add %
  return `${normalized}%`;
}

/**
 * Validate disk usage percentage value
 * @param usage Usage percentage string
 * @returns true if valid (numeric value between 0-100), false otherwise
 */
export function validateDiskUsage(usage: string): boolean {
  const numericValue = parseInt(usage.replace('%', ''), 10);
  return !isNaN(numericValue) && numericValue >= 0 && numericValue <= 100;
}
