/**
 * settings.ts
 *
 * Comprehensive settings management store for BirdNET-Go application configuration.
 * Handles all application settings including BirdNET AI parameters, integrations, and system config.
 *
 * Usage:
 * - Settings pages for configuration management
 * - Real-time settings updates across the application
 * - Change detection for unsaved modifications
 * - Centralized configuration state management
 *
 * Features:
 * - Complete settings state management
 * - Automatic change detection and persistence
 * - Optimistic UI updates with rollback on errors
 * - Debounced save operations
 * - Integration with settings API
 * - TypeScript interfaces for all setting types
 *
 * Settings Categories:
 * - Node: Basic node identification and location
 * - BirdNET: AI model parameters and thresholds
 * - Audio: Recording and processing settings
 * - Integrations: External service configurations
 * - Output: Data export and notification settings
 * - Security: Authentication and access control
 * - Debug: Development and troubleshooting options
 *
 * Change Detection:
 * - Tracks unsaved changes across all settings
 * - Provides dirty state indicators
 * - Handles form validation and error states
 * - Supports bulk save operations
 *
 * State Management:
 * - Centralized store for all configuration
 * - Reactive derived stores for change detection
 * - Automatic persistence to server
 * - Error handling and user feedback
 */
import { safeGet, safeSpread } from '$lib/utils/security';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import { coerceSettings } from '$lib/utils/settingsCoercion';
import { weatherDefaults } from '$lib/utils/weatherDefaults';
import { derived, get, writable } from 'svelte/store';
import { toastActions } from './toast.js';

// Type definitions for settings - Updated interfaces
export interface MainSettings {
  name: string;
  timeAs24h?: boolean;
  log?: {
    enabled: boolean;
    path: string;
    rotation: string;
    maxSize: number;
    rotationDay: string;
  };
}

export interface BirdNetSettings {
  modelPath: string | null;
  labelPath: string | null;
  sensitivity: number; // 0.0-1.5
  threshold: number; // 0.0-1.0
  overlap: number; // 0.0-2.9
  locale: string;
  threads: number;
  latitude: number;
  longitude: number;
  rangeFilter: RangeFilterSettings;
}

export interface DynamicThresholdSettings {
  enabled: boolean;
  debug: boolean;
  trigger: number;
  min: number;
  validHours: number;
}

export interface FalsePositiveFilterSettings {
  level: number; // 0=Off, 1=Lenient, 2=Moderate, 3=Balanced, 4=Strict, 5=Maximum
}

export interface RangeFilterSettings {
  threshold: number;
  speciesCount: number | null;
  species: string[];
}

export interface SQLiteSettings {
  enabled: boolean;
  path: string;
}

export interface MySQLSettings {
  enabled: boolean;
  username: string;
  password: string;
  database: string;
  host: string;
  port: string;
}

export interface OutputSettings {
  sqlite: SQLiteSettings;
  mysql: MySQLSettings;
}

export interface AudioSettings {
  source: string;
  ffmpegPath?: string;
  soxPath?: string;
  streamTransport?: string;
  export: ExportSettings;
  soundLevel: SoundLevelSettings;
  useAudioCore?: boolean;
  equalizer: EqualizerSettings;
}

export interface SoundLevelSettings {
  enabled: boolean;
  interval: number;
}

// Stream type constants
export const StreamTypes = {
  RTSP: 'rtsp',
  HTTP: 'http',
  HLS: 'hls',
  RTMP: 'rtmp',
  UDP: 'udp',
} as const;

export type StreamType = (typeof StreamTypes)[keyof typeof StreamTypes];

// StreamConfig represents a single audio stream source
export interface StreamConfig {
  name: string; // Required: descriptive name like "Front Yard"
  url: string; // Required: stream URL
  type: StreamType; // Stream type: rtsp, http, hls, rtmp, udp
  transport?: 'tcp' | 'udp'; // Transport protocol (for RTSP/RTMP only)
}

// RTSPHealthSettings matches backend RTSPHealthSettings
export interface RTSPHealthSettings {
  healthyDataThreshold: number; // seconds before stream considered unhealthy (default: 60)
  monitoringInterval: number; // health check interval in seconds (default: 30)
}

// RTSPSettings matches backend RTSPSettings - now uses StreamConfig
export interface RTSPSettings {
  streams: StreamConfig[]; // Stream configurations
  health?: RTSPHealthSettings; // health monitoring settings
  ffmpegParameters?: string[]; // optional custom FFmpeg parameters
}

export interface AudioQuality {
  sampleRate: number;
  bitRate: number;
  channels: number;
}

export interface EqualizerSettings {
  enabled: boolean;
  filters: EqualizerFilter[];
}

/** Supported equalizer filter types (must match backend eqfilter_config.go) */
export type EqualizerFilterType = 'HighPass' | 'LowPass' | 'BandReject';

export interface EqualizerFilter {
  id: string;
  type: EqualizerFilterType;
  frequency: number;
  q?: number;
  width?: number; // Bandwidth in Hz (for BandReject)
  gain?: number;
  passes?: number; // Number of filter passes (1=12dB, 2=24dB, 3=36dB, 4=48dB)
}

export interface ExportSettings {
  type: 'wav' | 'mp3' | 'flac' | 'aac' | 'opus';
  bitrate: string;
  enabled: boolean;
  debug?: boolean;
  path: string;
  retention: RetentionSettings;
  length: number; // audio capture length in seconds
  preCapture: number; // pre-capture in seconds
  gain: number; // gain in dB for audio capture
  normalization: NormalizationSettings; // audio normalization settings (EBU R128)
}

export interface NormalizationSettings {
  enabled: boolean; // true to enable loudness normalization
  targetLUFS: number; // target integrated loudness in LUFS (default: -23)
  loudnessRange: number; // loudness range in LU (default: 7)
  truePeak: number; // true peak limit in dBTP (default: -2)
}

export interface RetentionSettings {
  policy: string;
  maxAge: string;
  maxUsage: string;
  minClips: number;
  keepSpectrograms: boolean;
  enabled?: boolean; // legacy, might be present in old data
  maxSize?: number; // legacy, might be present in old data
}

export interface FilterSettings {
  privacy: PrivacyFilterSettings;
  dogBark: DogBarkFilterSettings;
}

export interface PrivacyFilterSettings {
  enabled: boolean;
  confidence: number;
  debug: boolean;
}

export interface PrivacyFilter {
  id: string;
  type: 'location' | 'time' | 'species' | 'confidence';
  name: string;
  enabled: boolean;
  threshold: number;
  conditions: Record<string, unknown>;
}

export interface DogBarkFilterSettings {
  enabled: boolean;
  confidence: number;
  remember: number;
  debug: boolean;
  species: string[];
}

export interface IntegrationSettings {
  birdweather: BirdWeatherSettings;
  mqtt: MQTTSettings;
  observability: ObservabilitySettings;
  weather: WeatherSettings;
}

export interface BirdWeatherSettings {
  enabled: boolean;
  id: string;
  latitude: number;
  longitude: number;
  locationAccuracy: number;
  threshold: number;
  debug: boolean;
}

export interface HomeAssistantSettings {
  enabled: boolean;
  discoveryPrefix: string; // Topic prefix (default: "homeassistant")
  deviceName: string; // Base device name (default: "BirdNET-Go")
}

export interface MQTTSettings {
  enabled: boolean;
  broker: string;
  port: number;
  username?: string;
  password?: string;
  topic: string;
  retain?: boolean;
  tls: {
    enabled: boolean;
    skipVerify: boolean;
  };
  homeAssistant?: HomeAssistantSettings;
}

export interface ObservabilitySettings {
  prometheus: {
    enabled: boolean;
    port: number;
    path: string;
  };
}

export interface OpenWeatherSettings {
  enabled: boolean;
  apiKey: string;
  endpoint: string;
  units: string;
  language: string;
}

export interface WundergroundSettings {
  apiKey: string;
  stationId: string;
  endpoint: string;
  units: 'm' | 'e' | 'h'; // m=metric, e=imperial/english, h=UK hybrid
}

export interface WeatherSettings {
  provider: 'none' | 'yrno' | 'openweather' | 'wunderground';
  pollInterval: number;
  debug: boolean;
  openWeather: OpenWeatherSettings;
  wunderground: WundergroundSettings;
}

// New array-based OAuth provider configuration
export interface OAuthProviderConfig {
  provider: 'google' | 'github' | 'microsoft' | 'line' | 'kakao';
  enabled: boolean;
  clientId: string;
  clientSecret: string;
  redirectUri?: string;
  userId?: string;
}

export interface SecuritySettings {
  baseUrl: string;
  host: string;
  autoTls: boolean;
  basicAuth: {
    enabled: boolean;
    username: string;
    password: string;
  };
  // New array-based OAuth provider configuration
  oauthProviders?: OAuthProviderConfig[];
  // Legacy OAuth settings (deprecated, kept for backwards compatibility)
  googleAuth?: OAuthSettings;
  githubAuth?: OAuthSettings;
  microsoftAuth?: OAuthSettings;
  allowSubnetBypass: {
    enabled: boolean;
    subnet: string;
  };
}

// Legacy OAuth settings interface (deprecated)
export interface OAuthSettings {
  enabled: boolean;
  clientId: string;
  clientSecret: string;
  redirectURI?: string;
  userId?: string;
}

export interface SpeciesSettings {
  include: string[];
  exclude: string[];
  config: Record<string, SpeciesConfig>;
}

export interface SpeciesConfig {
  threshold: number;
  interval: number;
  actions: Action[];
}

export interface Action {
  type: 'ExecuteCommand';
  command: string;
  parameters: string[];
  executeDefaults: boolean;
}

// Season definition for seasonal tracking
export interface Season {
  startMonth: number; // 1-12
  startDay: number; // 1-31
}

// Yearly tracking settings
export interface YearlyTrackingSettings {
  enabled: boolean;
  resetMonth: number; // Month to reset yearly tracking (1=January)
  resetDay: number; // Day to reset yearly tracking
  windowDays: number; // Days to show "new this year" indicator
}

// Seasonal tracking settings
export interface SeasonalTrackingSettings {
  enabled: boolean;
  windowDays: number; // Days to show "new this season" indicator
  seasons: Record<string, Season>; // Season definitions (e.g., spring, summer, fall, winter)
}

// Species tracking settings
export interface SpeciesTrackingSettings {
  enabled: boolean;
  newSpeciesWindowDays: number; // Days to consider a species "new"
  syncIntervalMinutes: number; // Interval to sync with database
  notificationSuppressionHours: number; // Hours to suppress duplicate notifications
  yearlyTracking: YearlyTrackingSettings;
  seasonalTracking: SeasonalTrackingSettings;
}

export interface SupportSettings {
  sentry: {
    enabled: boolean;
    dsn: string;
    environment: string;
    includePrivateInfo: boolean;
  };
}

// Realtime settings matching backend structure
export interface RealtimeSettings {
  interval?: number;
  processingTime?: boolean;
  audio?: AudioSettings;
  dashboard?: Dashboard;
  dynamicThreshold?: DynamicThresholdSettings;
  falsePositiveFilter?: FalsePositiveFilterSettings;
  log?: {
    enabled: boolean;
    path: string;
  };
  birdweather?: BirdWeatherSettings;
  privacyFilter?: PrivacyFilterSettings;
  dogBarkFilter?: DogBarkFilterSettings;
  rtsp?: RTSPSettings;
  mqtt?: MQTTSettings;
  telemetry?: TelemetrySettings;
  monitoring?: MonitoringSettings;
  species?: SpeciesSettings;
  weather?: WeatherSettings;
  speciesTracking?: SpeciesTrackingSettings;
}

// WebServer settings
export interface WebServerSettings {
  port?: string;
  log?: LogConfig;
  liveStream?: LiveStreamSettings;
  enableTerminal?: boolean; // Enable browser terminal (security risk - disabled by default)
}

// Dashboard settings
export interface Dashboard {
  thumbnails: Thumbnails;
  summaryLimit: number;
  locale?: string; // UI locale setting
  spectrogram?: SpectrogramPreRender; // Spectrogram pre-rendering settings
  temperatureUnit?: string; // Temperature display unit: "celsius" or "fahrenheit"
}

export interface Thumbnails {
  debug?: boolean;
  summary: boolean;
  recent: boolean;
  imageProvider: string;
  fallbackPolicy: string;
}

// Spectrogram size options
export type SpectrogramSize = 'sm' | 'md' | 'lg' | 'xl';

// Spectrogram generation mode options
export type SpectrogramMode = 'auto' | 'prerender' | 'user-requested';

// Spectrogram style preset options
export type SpectrogramStyle = 'default' | 'scientific_dark' | 'high_contrast_dark' | 'scientific';

// Spectrogram dynamic range preset options (dB values for Sox -z parameter)
// Lower values = higher contrast (weak signals visible), higher values = more detail
export type SpectrogramDynamicRange = '80' | '100' | '120';

// SpectrogramPreRender contains settings for spectrogram generation modes.
// Three modes control when and how spectrograms are generated:
//   - "auto": Generate on-demand when API is called (default, suitable for most systems)
//   - "prerender": Background worker generates during audio clip save (continuous CPU usage)
//   - "user-requested": Only generate when user clicks button in UI (zero automatic overhead)
export interface SpectrogramPreRender {
  mode?: SpectrogramMode; // Generation mode (default: 'auto')
  enabled?: boolean; // DEPRECATED: Use mode instead (kept for backward compatibility)
  size: SpectrogramSize; // Default size for all modes (sm=400px, md=800px, lg=1000px, xl=1200px)
  raw: boolean; // Generate raw spectrogram without axes/legend (default: true)
  style?: SpectrogramStyle; // Visual style preset (default: 'default')
  dynamicRange?: SpectrogramDynamicRange; // Dynamic range in dB: 80 (high contrast), 100 (standard), 120 (extended)
}

// Default spectrogram settings
export const DEFAULT_SPECTROGRAM_SETTINGS: SpectrogramPreRender = {
  mode: 'auto',
  enabled: false,
  size: 'sm',
  raw: true,
  style: 'default',
  dynamicRange: '100',
} as const;

// Log config
export interface LogConfig {
  enabled: boolean;
  path: string;
  level?: string;
  rotation?: string;
  maxSize?: number;
  rotationDay?: string;
}

// Telemetry settings
export interface TelemetrySettings {
  enabled: boolean;
  listen?: string; // e.g., "0.0.0.0:8090"
}

// Monitoring settings
export interface MonitoringSettings {
  enabled: boolean;
  interval: number;
}

// RTSP settings - using existing RTSPSettings interface above

// Live stream settings
export interface LiveStreamSettings {
  enabled?: boolean;
}

// File output settings (legacy interface)
export interface FileOutputSettings {
  file?: {
    enabled: boolean;
    path: string;
    type: string;
  };
}

// Backup settings
export interface BackupSettings {
  enabled?: boolean;
  interval?: string;
  path?: string;
}

// Sentry settings (was SupportSettings)
export interface SentrySettings {
  enabled: boolean;
  dsn?: string;
  environment?: string;
  includePrivateInfo?: boolean;
}

// Push notification provider filter configuration
export interface PushFilterConfig {
  types?: string[]; // 'detection', 'error', 'warning', 'info', 'system'
  priorities?: string[];
  components?: string[];
}

// Webhook authentication configuration
export interface WebhookAuthConfig {
  type: 'none' | 'bearer' | 'basic' | 'custom';
  token?: string; // For bearer auth
  user?: string; // For basic auth
  pass?: string; // For basic auth
  header?: string; // For custom auth
  value?: string; // For custom auth
}

// Webhook endpoint configuration
export interface WebhookEndpointConfig {
  url: string;
  method?: string; // POST, PUT, PATCH (default: POST)
  headers?: Record<string, string>;
  timeout?: string; // Duration string, e.g. "30s"
  auth?: WebhookAuthConfig;
  [key: string]: unknown; // passthrough for backend-only fields
}

// Push provider configuration (shoutrrr type for user-friendly UI)
export interface PushProviderConfig {
  type: 'shoutrrr' | 'webhook' | 'script';
  enabled: boolean;
  name: string;
  urls?: string[]; // For shoutrrr providers
  endpoints?: WebhookEndpointConfig[]; // For webhook providers
  filter?: PushFilterConfig;
  timeout?: string; // Duration string, e.g. "30s"
  // Allow additional backend-only fields to pass through
  [key: string]: unknown;
}

// Push settings for notification delivery
export interface PushSettings {
  enabled: boolean;
  providers?: PushProviderConfig[];
  // Detection filtering settings
  minConfidenceThreshold?: number; // 0.0-1.0, 0 = disabled
  speciesCooldownMinutes?: number; // 0 = disabled
  // Allow additional backend-only fields to pass through (default_timeout, circuit_breaker, etc.)
  [key: string]: unknown;
}

// Notification templates
export interface NewSpeciesTemplate {
  title: string;
  message: string;
}

export interface NotificationTemplates {
  newSpecies?: NewSpeciesTemplate;
}

// Main notification settings structure
export interface NotificationSettings {
  push?: PushSettings;
  templates?: NotificationTemplates;
}

// Main settings form data interface - EXACTLY matching backend structure
export interface SettingsFormData {
  debug?: boolean;
  version?: string;
  buildDate?: string;
  systemId?: string;
  main: MainSettings;
  birdnet: BirdNetSettings;
  input?: unknown; // Not exposed via JSON
  realtime?: RealtimeSettings;
  webServer?: WebServerSettings;
  security?: SecuritySettings;
  sentry?: SentrySettings;
  output?: OutputSettings;
  backup?: BackupSettings;
  notification?: NotificationSettings;
}

// Global settings state interface
export interface GlobalSettingsState {
  formData: SettingsFormData;
  originalData: SettingsFormData;
  isLoading: boolean;
  isSaving: boolean;
  activeSection: string;
  error: string | null;
}

// API response types
export interface APIResponse<T> {
  success: boolean;
  data?: T;
  error?: string;
  errors?: Record<string, string[]>;
}

export interface TestResult {
  success: boolean;
  message: string;
  details?: Record<string, unknown>;
}

// Initialize empty settings data
function createEmptySettings(): SettingsFormData {
  return {
    main: {
      name: '',
    },
    birdnet: {
      modelPath: '',
      labelPath: '',
      sensitivity: 1.0,
      threshold: 0.3,
      overlap: 0.0,
      locale: 'en',
      threads: 4,
      latitude: 0,
      longitude: 0,
      rangeFilter: {
        threshold: 0.03,
        speciesCount: null,
        species: [],
      },
    },
    realtime: {
      interval: 15,
      processingTime: false,
      dynamicThreshold: {
        enabled: false,
        debug: false,
        trigger: 0.8,
        min: 0.3,
        validHours: 24,
      },
      falsePositiveFilter: {
        level: 0,
      },
      audio: {
        source: '',
        ffmpegPath: '',
        soxPath: '',
        streamTransport: 'auto',
        export: {
          type: 'wav',
          bitrate: '96k',
          enabled: false,
          debug: false,
          path: 'clips/',
          retention: {
            policy: 'none',
            maxAge: '7d',
            maxUsage: '80%',
            minClips: 10,
            keepSpectrograms: false,
          },
          length: 15, // Default 15 seconds capture length
          preCapture: 3, // Default 3 seconds pre-capture
          gain: 0, // Default 0 dB gain (no amplification)
          normalization: {
            enabled: false, // Disabled by default
            targetLUFS: -23.0, // EBU R128 broadcast standard
            loudnessRange: 7.0, // Typical range for broadcast
            truePeak: -2.0, // Headroom to prevent clipping
          },
        },
        soundLevel: {
          enabled: false,
          interval: 60,
        },
        equalizer: {
          enabled: false,
          filters: [],
        },
      },
      privacyFilter: {
        enabled: false,
        confidence: 0.5,
        debug: false,
      },
      dogBarkFilter: {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      },
      birdweather: {
        enabled: false,
        id: '',
        latitude: 0,
        longitude: 0,
        locationAccuracy: 1000,
        threshold: 0.7,
        debug: false,
      },
      mqtt: {
        enabled: false,
        broker: '',
        port: 1883,
        topic: 'birdnet',
        retain: false,
        tls: {
          enabled: false,
          skipVerify: false,
        },
        homeAssistant: {
          enabled: false,
          discoveryPrefix: 'homeassistant',
          deviceName: 'BirdNET-Go',
        },
      },
      species: {
        include: [],
        exclude: [],
        config: {},
      },
      weather: weatherDefaults,
      dashboard: {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
        spectrogram: DEFAULT_SPECTROGRAM_SETTINGS,
        temperatureUnit: 'celsius',
      },
    },
    webServer: {},
    security: {
      baseUrl: '',
      host: '',
      autoTls: false,
      basicAuth: {
        enabled: false,
        username: '',
        password: '',
      },
      // New array-based OAuth providers (replaces legacy individual provider fields)
      oauthProviders: [],
      allowSubnetBypass: {
        enabled: false,
        subnet: '',
      },
    },
    sentry: {
      enabled: false,
      dsn: '',
      environment: 'production',
      includePrivateInfo: false,
    },
    output: {
      sqlite: {
        enabled: false,
        path: 'birdnet.db',
      },
      mysql: {
        enabled: false,
        username: '',
        password: '',
        database: '',
        host: 'localhost',
        port: '3306',
      },
    },
    notification: {
      push: {
        enabled: false,
        providers: [],
      },
      templates: {
        newSpecies: {
          title: '',
          message: '',
        },
      },
    },
  };
}

// Main settings store
const initialState: GlobalSettingsState = {
  formData: createEmptySettings(),
  originalData: createEmptySettings(),
  isLoading: false,
  isSaving: false,
  activeSection: 'main',
  error: null,
};

export const settingsStore = writable<GlobalSettingsState>(initialState);

// Derived stores for easy component usage
export const hasUnsavedChanges = derived(
  settingsStore,
  $store => JSON.stringify($store.formData) !== JSON.stringify($store.originalData)
);

export const currentSection = derived(settingsStore, $store => $store.activeSection);

export const isLoading = derived(settingsStore, $store => $store.isLoading);

export const isSaving = derived(settingsStore, $store => $store.isSaving);

// Section-specific derived stores matching backend structure
export const mainSettings = derived(settingsStore, $store => $store.formData.main);

export const birdnetSettings = derived(settingsStore, $store => $store.formData.birdnet);

export const realtimeSettings = derived(settingsStore, $store => $store.formData.realtime);

export const audioSettings = derived(settingsStore, $store => $store.formData.realtime?.audio);

export const privacyFilterSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.privacyFilter
);

export const dogBarkFilterSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.dogBarkFilter
);

export const birdweatherSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.birdweather
);

export const mqttSettings = derived(settingsStore, $store => $store.formData.realtime?.mqtt);

export const homeAssistantSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.mqtt?.homeAssistant
);

export const speciesSettings = derived(settingsStore, $store => $store.formData.realtime?.species);

export const dashboardSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.dashboard
);

export const securitySettings = derived(settingsStore, $store => $store.formData.security);

export const sentrySettings = derived(settingsStore, $store => $store.formData.sentry);

export const rtspSettings = derived(settingsStore, $store => $store.formData.realtime?.rtsp);

export const outputSettings = derived(settingsStore, $store => $store.formData.output);

export const notificationSettings = derived(settingsStore, $store => $store.formData.notification);

export const integrationSettings = derived(settingsStore, $store => ({
  birdweather: $store.formData.realtime?.birdweather,
  mqtt: $store.formData.realtime?.mqtt,
  observability: {
    prometheus: {
      enabled: $store.formData.realtime?.telemetry?.enabled ?? false,
      port: $store.formData.realtime?.telemetry?.listen
        ? parseInt($store.formData.realtime.telemetry.listen.split(':')[1] || '8090')
        : 8090,
      path: '/metrics',
    },
  },
  weather: $store.formData.realtime?.weather,
}));

export const supportSettings = derived(settingsStore, $store => ({
  sentry: $store.formData.sentry,
}));

// Dynamic threshold settings derived store
export const dynamicThresholdSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.dynamicThreshold
);

// Species tracking settings derived store
export const speciesTrackingSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.speciesTracking
);

// Settings actions
export const settingsActions = {
  async loadSettings() {
    settingsStore.update(state => ({ ...state, isLoading: true, error: null }));
    try {
      const data = await settingsAPI.load();
      const mergedData = { ...createEmptySettings(), ...data };

      // Apply coercion to each section
      const coercedData = { ...mergedData } as SettingsFormData;
      for (const [section, sectionData] of Object.entries(mergedData)) {
        if (sectionData && typeof sectionData === 'object') {
          const coercedSection = coerceSettings(section, sectionData as Record<string, unknown>);
          // eslint-disable-next-line security/detect-object-injection -- Safe: section from Object.entries of known object
          (coercedData as unknown as Record<string, unknown>)[section] = coercedSection;
        }
      }

      settingsStore.update(state => ({
        ...state,
        formData: coercedData,
        originalData: JSON.parse(JSON.stringify(coercedData)),
        isLoading: false,
      }));
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Failed to load settings';
      settingsStore.update(state => ({
        ...state,
        isLoading: false,
        error: errorMessage,
      }));
      throw error;
    }
  },

  updateSection<K extends keyof SettingsFormData>(section: K, data: Partial<SettingsFormData[K]>) {
    settingsStore.update(state => {
      const currentSectionData = safeGet(
        state.formData,
        section as string,
        {} as SettingsFormData[K]
      );
      const mergedData = safeSpread(currentSectionData, data) as SettingsFormData[K];

      // Apply coercion immediately to ensure values are always within valid ranges
      // This is especially important for NumberField components that need instant validation
      const coercedData = coerceSettings(
        section as string,
        mergedData as Record<string, unknown>
      ) as SettingsFormData[K];

      return {
        ...state,
        formData: {
          ...state.formData,
          [section]: coercedData,
        },
      };
    });
  },

  async saveSettings() {
    settingsStore.update(state => ({ ...state, isSaving: true, error: null }));
    try {
      const currentState = get(settingsStore);

      // Apply coercion to all sections before saving
      const coercedFormData = { ...currentState.formData };
      for (const [section, data] of Object.entries(coercedFormData)) {
        if (data && typeof data === 'object') {
          const key = section as keyof SettingsFormData;
          // Use a type assertion to handle the assignment
          // eslint-disable-next-line security/detect-object-injection -- key is from controlled source
          (coercedFormData as Record<string, unknown>)[key] = coerceSettings(
            section,
            data as Record<string, unknown>
          );
        }
      }

      // Convert empty strings to null for modelPath and labelPath to signal "revert to default"
      // This ensures the config file is properly cleaned when users clear these fields
      // Trim whitespace to handle cases like "   " which should also be treated as empty
      if (coercedFormData.birdnet.modelPath?.trim() === '') {
        coercedFormData.birdnet.modelPath = null;
      }
      if (coercedFormData.birdnet.labelPath?.trim() === '') {
        coercedFormData.birdnet.labelPath = null;
      }

      await settingsAPI.save(coercedFormData);

      // Check if UI locale changed and apply it
      const newLocale = currentState.formData.realtime?.dashboard?.locale;
      if (newLocale) {
        // Dynamically import i18n functions to avoid circular dependencies
        const { getLocale, setLocale, isValidLocale } = await import('$lib/i18n/index.js');
        const currentLocale = getLocale();
        if (newLocale !== currentLocale && isValidLocale(newLocale)) {
          setLocale(newLocale);
        }
      }

      // Update originalData to match the saved formData (no reload needed)
      settingsStore.update(state => ({
        ...state,
        originalData: JSON.parse(JSON.stringify(state.formData)),
        isSaving: false,
      }));

      // Show success toast
      toastActions.success('Settings saved successfully');
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Failed to save settings';
      settingsStore.update(state => ({
        ...state,
        isSaving: false,
        error: errorMessage,
      }));

      // Show error toast
      toastActions.error(errorMessage);

      throw error;
    }
  },

  resetSection<K extends keyof SettingsFormData>(section: K) {
    settingsStore.update(state => {
      const originalSectionData = safeGet(state.originalData, section as string);
      return {
        ...state,
        formData: {
          ...state.formData,
          [section]: originalSectionData ? JSON.parse(JSON.stringify(originalSectionData)) : {},
        },
      };
    });
  },

  resetAllSettings() {
    settingsStore.update(state => ({
      ...state,
      formData: JSON.parse(JSON.stringify(state.originalData)),
    }));
  },

  setActiveSection(section: string) {
    settingsStore.update(state => ({
      ...state,
      activeSection: section,
    }));
  },

  clearError() {
    settingsStore.update(state => ({
      ...state,
      error: null,
    }));
  },
};
