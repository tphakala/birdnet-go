export interface LiveSpectrogramColumn {
  tUnixMs: number;
  bins: number[];
}

export interface LiveSpectrogramMeta {
  type: 'spectrogram-meta';
  sourceId: string;
  streamEpochUnixMs: number;
  sampleRate: number;
  fftSize: number;
  hopSize: number;
  window: string;
  binCount: number;
  minFrequencyHz: number;
  maxFrequencyHz: number;
  batchIntervalMs: number;
}

export interface LiveSpectrogramColumnsEvent {
  type: 'spectrogram-columns';
  sourceId: string;
  columns: LiveSpectrogramColumn[];
}
