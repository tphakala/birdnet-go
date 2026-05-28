import { t } from '$lib/i18n';
import { api } from '$lib/utils/api';
import type { ChannelMode, ChannelAnalysis } from '$lib/stores/settings';

export const streamTypeOptions = [
  { value: 'rtsp', label: 'RTSP' },
  { value: 'http', label: 'HTTP' },
  { value: 'hls', label: 'HLS' },
  { value: 'rtmp', label: 'RTMP' },
  { value: 'udp', label: 'UDP/RTP' },
];

export const transportOptions = [
  { value: 'tcp', label: 'TCP' },
  { value: 'udp', label: 'UDP' },
];

type ChannelModeOption = { value: ChannelMode; label: () => string };

export const channelModeOptions: ChannelModeOption[] = [
  {
    value: 'downmix',
    label: () => t('settings.audio.streams.channelMode.downmix'),
  },
  { value: 'left', label: () => t('settings.audio.streams.channelMode.left') },
  { value: 'right', label: () => t('settings.audio.streams.channelMode.right') },
];

export async function analyzeStreamChannels(url: string): Promise<ChannelAnalysis> {
  return api.post<ChannelAnalysis>('/api/v2/streams/analyze-channels', {
    url: url.trim(),
  });
}
