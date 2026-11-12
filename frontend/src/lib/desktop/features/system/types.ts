export interface ModelMetrics {
  id: string;
  name: string;
  metricKey: string;
  chunkSeconds: number;
  history: number[];
  color: string;
}

/** One row of GET /api/v2/system/processes. */
export interface ProcessInfo {
  pid: number;
  name: string;
  /** gopsutil state word such as running, sleep or zombie; unknown when the OS reports none */
  status: string;
  /** CPU share in percent */
  cpu: number;
  /** Resident memory in bytes */
  memory: number;
  /** Seconds since the process started */
  uptime: number;
}
