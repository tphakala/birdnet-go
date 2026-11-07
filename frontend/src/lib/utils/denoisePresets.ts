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
