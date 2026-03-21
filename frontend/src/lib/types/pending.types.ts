/** Status of a pending detection in the "currently hearing" lifecycle. */
export type PendingDetectionStatus = 'active' | 'approved' | 'rejected';

/** A single pending detection as received from the SSE `pending` event. */
export interface PendingDetection {
  /** Common name of the species */
  species: string;
  /** Scientific name (used for thumbnail lookup) */
  scientificName: string;
  /** Bird image URL */
  thumbnail: string;
  /** Lifecycle status */
  status: PendingDetectionStatus;
  /** Unix timestamp (seconds) when species was first detected */
  firstDetected: number;
  /** Unix timestamp (seconds) when the source audio chunk was captured (for spectrogram overlay alignment) */
  audioCapturedAt?: number;
  /** Unix timestamp (seconds) of the most recent inference hit for this species */
  lastUpdated?: number;
  /** Audio source display name */
  source: string;
  /** Raw source ID for filtering detections by active stream */
  sourceID: string;
  /** Number of inference hits accumulated for this pending detection */
  hitCount?: number;
}
