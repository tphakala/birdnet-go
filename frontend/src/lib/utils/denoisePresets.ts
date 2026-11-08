/**
 * Clip denoise presets offered in the audio player, ordered from off to the
 * strongest. The ids match the backend preset names (`conf.DenoisePreset*`);
 * the empty id means denoise is off.
 */
export interface DenoiseOption {
  id: string;
  labelKey: string;
}

export const DENOISE_OPTIONS: readonly DenoiseOption[] = [
  { id: '', labelKey: 'components.audioPlayer.processing.denoiseOff' },
  { id: 'light', labelKey: 'components.audioPlayer.processing.denoiseLight' },
  { id: 'light-medium', labelKey: 'components.audioPlayer.processing.denoiseLightMedium' },
  { id: 'medium', labelKey: 'components.audioPlayer.processing.denoiseMedium' },
  { id: 'heavy', labelKey: 'components.audioPlayer.processing.denoiseHeavy' },
];

/** The selectable strengths: every option except the "off" entry. */
export const DENOISE_STRENGTH_OPTIONS: readonly DenoiseOption[] = DENOISE_OPTIONS.filter(
  option => option.id !== ''
);

/**
 * The strength a denoise setting starts with when it is switched on without a
 * choice. Matches the backend `conf.DefaultDenoisePreset`.
 */
export const DEFAULT_DENOISE_PRESET = 'light-medium';

/** Whether id names a real strength (the empty "off" id is not one). */
export function isDenoisePresetId(id: unknown): id is string {
  return typeof id === 'string' && DENOISE_STRENGTH_OPTIONS.some(option => option.id === id);
}
