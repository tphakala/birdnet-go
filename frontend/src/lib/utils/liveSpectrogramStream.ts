import ReconnectingEventSource from 'reconnecting-eventsource';

import { buildAppUrl } from '$lib/utils/urlHelpers';
import { loggers } from '$lib/utils/logger';
import type { LiveSpectrogramColumnsEvent, LiveSpectrogramMeta } from '$lib/types/liveSpectrogram';

const logger = loggers.audio;

interface LiveSpectrogramHandlers {
  onMeta?: (_meta: LiveSpectrogramMeta) => void;
  onColumns?: (_event: LiveSpectrogramColumnsEvent) => void;
}

export function connectLiveSpectrogramStream(
  sourceId: string,
  handlers: LiveSpectrogramHandlers
): () => void {
  const encodedSourceId = encodeURIComponent(sourceId);
  const eventSource = new ReconnectingEventSource(
    buildAppUrl(`/api/v2/streams/spectrogram/${encodedSourceId}`),
    {
      max_retry_time: 30000,
      withCredentials: false,
    }
  );

  eventSource.addEventListener('spectrogram-meta', (event: Event) => {
    try {
      const messageEvent = event as MessageEvent;
      handlers.onMeta?.(JSON.parse(messageEvent.data) as LiveSpectrogramMeta);
    } catch (error) {
      logger.error('Failed to parse live spectrogram metadata', error);
    }
  });

  eventSource.addEventListener('spectrogram-columns', (event: Event) => {
    try {
      const messageEvent = event as MessageEvent;
      handlers.onColumns?.(JSON.parse(messageEvent.data) as LiveSpectrogramColumnsEvent);
    } catch (error) {
      logger.error('Failed to parse live spectrogram columns', error);
    }
  });

  return () => eventSource.close();
}
