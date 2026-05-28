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

export const channelModeOptions = [
  {
    value: 'downmix' as ChannelMode,
    label: () => t('settings.audio.streams.channelMode.downmix'),
  },
  { value: 'left' as ChannelMode, label: () => t('settings.audio.streams.channelMode.left') },
  { value: 'right' as ChannelMode, label: () => t('settings.audio.streams.channelMode.right') },
];

export async function analyzeStreamChannels(url: string): Promise<ChannelAnalysis> {
  return api.post<ChannelAnalysis>('/api/v2/streams/analyze-channels', {
    url: url.trim(),
  });
}
