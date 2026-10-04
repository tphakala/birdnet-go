import { api } from './api.js';
import type {
  SettingsFormData,
  TestResult,
  BirdWeatherSettings,
  BirdNetSettings,
  Dashboard,
  AudioSettings,
  RTSPSettings,
  PrivacyFilterSettings,
  SentrySettings,
  MQTTSettings,
  RangeFilterSpeciesEntry,
} from '$lib/stores/settings.js';

export interface TLSCertificateInfo {
  installed: boolean;
  mode?: string;
  subject?: string;
  issuer?: string;
  notBefore?: string;
  notAfter?: string;
  daysUntilExpiry?: number;
  sans?: string[];
  serialNumber?: string;
  fingerprint?: string;
}

export interface TLSCertificateUpload {
  certificate: string;
  privateKey: string;
  caCertificate?: string;
}

export interface TLSGenerateRequest {
  validity?: string;
}

export interface MQTTTLSCertificateInfo {
  ca: TLSCertificateInfo;
  client: TLSCertificateInfo;
  hasKey: boolean;
}

export interface MQTTTLSCertificateUpload {
  caCertificate?: string;
  clientCertificate?: string;
  clientKey?: string;
}

/** Base endpoint for the settings API. */
const SETTINGS_ENDPOINT = '/api/v2/settings';

/**
 * Keys the backend never lets the API change (getBlockedFieldMap in
 * internal/api/v2/settings.go): it reverts them and reports them in
 * skippedFields. They are left out of the section payloads so a section save
 * never sends one, and the store never records a value the server refused.
 * rangeFilter is left out whole because it holds the blocked model, species
 * and lastUpdated fields.
 */
type BirdNetServerOwnedKey = 'rangeFilter';
type AudioServerOwnedKey = 'ffmpegPath' | 'soxPath';

/**
 * Request bodies for the per-section settings update, keyed by the lowercase
 * backend section name. Partial is shallow, so a nested value is typed as a
 * complete object; the backend itself merges nested objects key by key and only
 * replaces arrays. Extend this when another caller needs a section; the
 * backend accepts more sections than are listed here.
 */
export interface SettingsSectionPayloads {
  birdnet: Partial<Omit<BirdNetSettings, BirdNetServerOwnedKey>>;
  dashboard: Partial<Dashboard>;
  audio: Partial<Omit<AudioSettings, AudioServerOwnedKey>>;
  rtsp: Partial<RTSPSettings>;
  privacyfilter: Partial<PrivacyFilterSettings>;
  birdweather: Partial<BirdWeatherSettings>;
  sentry: Partial<SentrySettings>;
}

/** Backend section names accepted by settingsAPI.patchSection. */
export type SettingsSectionName = keyof SettingsSectionPayloads;

/** The fields of the section PATCH response that the store reads. */
export interface SettingsSectionPatchResponse {
  /** Fields in the request that the backend refused to change. */
  skippedFields?: string[] | null;
}

/**
 * Settings API client extending the base API client
 */
export const settingsAPI = {
  /**
   * Load all settings from the server
   */
  load: (): Promise<SettingsFormData> => {
    return api.get<SettingsFormData>(SETTINGS_ENDPOINT);
  },

  /**
   * Save all settings to the server
   */
  save: (data: SettingsFormData): Promise<unknown> => {
    return api.put<unknown>(SETTINGS_ENDPOINT, data);
  },

  /**
   * Save a single backend section with PATCH /api/v2/settings/:section. The
   * backend merges the body into the stored section, so keys the body omits keep
   * their saved values and no other section is touched.
   */
  patchSection: <S extends SettingsSectionName>(
    section: S,
    body: SettingsSectionPayloads[S]
  ): Promise<SettingsSectionPatchResponse | null> => {
    return api.patch<SettingsSectionPatchResponse | null>(`${SETTINGS_ENDPOINT}/${section}`, body);
  },

  /**
   * Test endpoints for validating settings
   */
  test: {
    /**
     * Test BirdWeather integration
     */
    birdweather: (config: BirdWeatherSettings): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/birdweather', config);
    },

    /**
     * Test MQTT connection
     */
    mqtt: (config: MQTTSettings): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/mqtt', config);
    },

    /**
     * Test database connection
     */
    database: (config: Record<string, unknown>): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/database', config);
    },

    /**
     * Test audio source
     */
    audio: (config: Record<string, unknown>): Promise<TestResult> => {
      return api.post<TestResult>('/api/v2/test/audio', config);
    },
  },

  /**
   * Range filter API calls
   */
  rangeFilter: {
    /**
     * Load species using the test endpoint that respects the current threshold.
     * Uses POST /api/v2/range/species/test (not GET /api/v2/range/species/list)
     * because the list endpoint ignores query parameters and returns all species.
     * See #2393.
     */
    testSpecies: (
      latitude: number,
      longitude: number,
      threshold: number
    ): Promise<{ count: number; species?: RangeFilterSpeciesEntry[] | null }> => {
      return api.post('/api/v2/range/species/test', {
        latitude,
        longitude,
        threshold,
      });
    },
  },

  /**
   * Species-related API calls
   */
  species: {
    /**
     * Search for species by query string
     */
    search: (query: string): Promise<string[]> => {
      const encodedQuery = encodeURIComponent(query);
      return api.get<string[]>(`/api/v2/species/search?q=${encodedQuery}`);
    },

    /**
     * Get species filtered by range/location
     */
    rangeFilter: (lat: number, lon: number): Promise<string[]> => {
      return api.get<string[]>(`/api/v2/species/range?lat=${lat}&lon=${lon}`);
    },

    /**
     * Get all available species
     */
    all: (): Promise<string[]> => {
      return api.get<string[]>('/api/v2/species');
    },
  },

  /**
   * System information endpoints
   */
  system: {
    /**
     * Get available audio devices
     */
    audioDevices: (): Promise<Array<{ id: string; name: string }>> => {
      return api.get('/api/v2/system/audio-devices');
    },

    /**
     * Check FFmpeg availability
     */
    ffmpegStatus: (): Promise<{ available: boolean; version?: string }> => {
      return api.get('/api/v2/system/ffmpeg');
    },
  },

  /**
   * TLS certificate management
   */
  tls: {
    getCertificate: (): Promise<TLSCertificateInfo> =>
      api.get<TLSCertificateInfo>('/api/v2/tls/certificate'),

    uploadCertificate: (data: TLSCertificateUpload): Promise<TLSCertificateInfo> =>
      api.post<TLSCertificateInfo>('/api/v2/tls/certificate', data),

    deleteCertificate: (): Promise<unknown> => api.delete('/api/v2/tls/certificate'),

    generateSelfSigned: (data?: TLSGenerateRequest): Promise<TLSCertificateInfo> =>
      api.post<TLSCertificateInfo>('/api/v2/tls/certificate/generate', data ?? {}),
  },

  /**
   * MQTT TLS certificate management
   */
  mqttTls: {
    getCertificates: (): Promise<MQTTTLSCertificateInfo> =>
      api.get<MQTTTLSCertificateInfo>('/api/v2/integrations/mqtt/tls/certificate'),

    uploadCertificates: (data: MQTTTLSCertificateUpload): Promise<MQTTTLSCertificateInfo> =>
      api.post<MQTTTLSCertificateInfo>('/api/v2/integrations/mqtt/tls/certificate', data),

    deleteCertificates: (): Promise<unknown> =>
      api.delete('/api/v2/integrations/mqtt/tls/certificate'),
  },

  /**
   * Configuration validation
   */
  validate: {
    /**
     * Validate entire settings configuration
     */
    all: (
      data: SettingsFormData
    ): Promise<{ valid: boolean; errors?: Record<string, string[]> }> => {
      return api.post('/api/v2/settings/validate', data);
    },

    /**
     * Validate specific section
     */
    section: (
      section: string,
      data: Record<string, unknown>
    ): Promise<{ valid: boolean; errors?: Record<string, string[]> }> => {
      return api.post(`/api/v2/settings/validate/${section}`, data);
    },
  },
};
