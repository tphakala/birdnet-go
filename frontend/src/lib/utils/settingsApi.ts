import { api } from './api.js';
import type {
  SettingsFormData,
  TestResult,
  BirdWeatherSettings,
  MQTTSettings,
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

/**
 * Settings API client extending the base API client
 */
export const settingsAPI = {
  /**
   * Load all settings from the server
   */
  load: (): Promise<SettingsFormData> => {
    return api.get<SettingsFormData>('/api/v2/settings');
  },

  /**
   * Save all settings to the server
   */
  save: (data: SettingsFormData): Promise<unknown> => {
    return api.put<unknown>('/api/v2/settings', data);
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

    /**
     * Generate system support dump
     */
    supportDump: (options: { includePrivateInfo: boolean }): Promise<{ downloadUrl: string }> => {
      return api.post('/api/v2/system/support-dump', options);
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
