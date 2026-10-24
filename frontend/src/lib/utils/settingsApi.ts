import { api } from './api.js';
import {
  withoutServerOwnedKeys,
  type AudioServerOwnedKey,
  type BirdNetServerOwnedKey,
} from './settingsSections';
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

/**
 * How the requester's own connection relates to security.trustedProxies, as
 * reported by GET /api/v2/settings/connection:
 * - `direct`: no client-IP header, the peer is the client.
 * - `trusted_proxy`: the peer is a listed proxy and one client address was verified.
 * - `trusted_proxy_unverified`: the peer is listed but its headers could not be verified.
 * - `untrusted_public_proxy`: a public peer sent client-IP headers; never offered for trust.
 * - `untrusted_proxy`: a local (loopback, link-local, private, CGNAT) peer sent client-IP
 *   headers and listing it would verify them.
 * - `untrusted_proxy_unverifiable`: headers were sent that listing the peer would not
 *   verify, or the peer address was unparseable.
 */
export type ConnectionStatus =
  | 'direct'
  | 'trusted_proxy'
  | 'trusted_proxy_unverified'
  | 'untrusted_public_proxy'
  | 'untrusted_proxy'
  | 'untrusted_proxy_unverifiable';

/** Response of GET /api/v2/settings/connection; describes the saved settings and the current request only. */
export interface ConnectionCheckResult {
  status: ConnectionStatus;
  /** The connection peer, or an empty string when it could not be parsed. */
  peerAddress: string;
  /** Whether the request carries any client-IP header. */
  hasForwardedHeaders: boolean;
  /** Whether the peer is inside a saved trusted proxy range. */
  peerIsTrustedProxy: boolean;
  /** The client address the authentication decision uses, or an empty string when none was verified. */
  verifiedClientAddress: string;
  /** Whether the peer is inside Cloudflare's published edge ranges, whatever the saved list says. */
  peerIsCloudflareEdge: boolean;
  /** Whether listing the peer together with the `cloudflare` preset would verify the headers that do not verify now. */
  cloudflarePresetWouldVerify: boolean;
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
 * Request bodies for the per-section settings update, keyed by the lowercase
 * backend section name. Partial is shallow, so a nested value is typed as a
 * complete object; the backend itself merges nested objects key by key and only
 * replaces arrays. Extend this when another caller needs a section; the
 * backend accepts more sections than are listed here. Keys the server owns
 * are left out (see settingsSections.ts).
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
   * Load all settings from the server. onETag receives the response ETag header
   * (null when absent), the revision a later save can pass as ifMatch.
   */
  load: (options?: { onETag?: (etag: string | null) => void }): Promise<SettingsFormData> => {
    return api.get<SettingsFormData>(SETTINGS_ENDPOINT, {
      onHeaders: headers => options?.onETag?.(headers.get('ETag')),
    });
  },

  /**
   * Save all settings to the server. ifMatch is sent as If-Match, so the server
   * answers 412 when the settings changed since the ETag was issued. onETag
   * receives the ETag of the response (null when absent), also for an HTTP
   * error, so a caller can read the current ETag a 412 names. It is only
   * called when ifMatch is set.
   */
  save: (
    data: SettingsFormData,
    options?: { ifMatch?: string; onETag?: (etag: string | null) => void }
  ): Promise<unknown> => {
    return api.put<unknown>(
      SETTINGS_ENDPOINT,
      data,
      options?.ifMatch
        ? {
            headers: { 'If-Match': options.ifMatch },
            onHeaders: headers => options.onETag?.(headers.get('ETag')),
          }
        : undefined
    );
  },

  /**
   * Save a single backend section with PATCH /api/v2/settings/:section. The
   * backend merges the body into the stored section, so keys the body omits keep
   * their saved values and no other section is touched. Keys the server owns
   * are dropped before sending.
   */
  patchSection: <S extends SettingsSectionName>(
    section: S,
    body: SettingsSectionPayloads[S]
  ): Promise<SettingsSectionPatchResponse | null> => {
    return api.patch<SettingsSectionPatchResponse | null>(
      `${SETTINGS_ENDPOINT}/${section}`,
      withoutServerOwnedKeys(section, body)
    );
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
  },

  /**
   * Security helpers that are not part of a settings section
   */
  security: {
    /**
     * Classify the requester's own connection against the saved trusted proxy
     * list. Read-only; reflects the saved settings, not unsaved form edits.
     */
    getConnection: (): Promise<ConnectionCheckResult> =>
      api.get<ConnectionCheckResult>('/api/v2/settings/connection'),
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
