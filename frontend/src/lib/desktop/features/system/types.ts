export interface ModelMetrics {
  id: string;
  name: string;
  metricKey: string;
  chunkSeconds: number;
  history: number[];
  color: string;
}
