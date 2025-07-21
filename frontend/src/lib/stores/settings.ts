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
import { writable, derived, get } from 'svelte/store';
import { settingsAPI } from '$lib/utils/settingsApi.js';
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
  modelPath: string;
  labelPath: string;
  sensitivity: number; // 0.0-1.5
  threshold: number; // 0.0-1.0
  overlap: number; // 0.0-2.9
  locale: string;
  threads: number;
  latitude: number;
  longitude: number;
  dynamicThreshold: DynamicThresholdSettings;
  rangeFilter: RangeFilterSettings;
  database: DatabaseSettings;
}

export interface DynamicThresholdSettings {
  enabled: boolean;
  debug: boolean;
  trigger: number;
  min: number;
  validHours: number;
}

export interface RangeFilterSettings {
  model: 'legacy' | 'latest';
  threshold: number;
  speciesCount: number | null;
  species: string[];
}

export interface DatabaseSettings {
  type: 'sqlite' | 'mysql';
  path: string; // For SQLite
  host: string; // For MySQL
  port: number; // For MySQL
  name: string; // For MySQL
  username: string; // For MySQL
  password: string; // For MySQL
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
  retention?: RetentionSettings;
}

export interface SoundLevelSettings {
  enabled: boolean;
  interval: number;
}

export interface RTSPSettings {
  transport: 'tcp' | 'udp';
  urls: RTSPUrl[];
}

export interface RTSPUrl {
  url: string;
  enabled: boolean;
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

export interface EqualizerFilter {
  id: string;
  type: 'highpass' | 'lowpass' | 'bandpass' | 'bandstop';
  frequency: number;
  q?: number;
  gain?: number;
}

export interface ExportSettings {
  type: 'wav' | 'mp3' | 'flac' | 'aac' | 'opus';
  bitrate: string;
  enabled: boolean;
  debug: boolean;
  path: string;
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

export interface WeatherSettings {
  provider: 'none' | 'yrno' | 'openweather';
  pollInterval: number;
  debug: boolean;
  openWeather: OpenWeatherSettings;
}

export interface SecuritySettings {
  host: string;
  autoTls: boolean;
  basicAuth: {
    enabled: boolean;
    username: string;
    password: string;
  };
  googleAuth: OAuthSettings;
  githubAuth: OAuthSettings;
  allowSubnetBypass: {
    enabled: boolean;
    subnet: string;
  };
}

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

export interface SupportSettings {
  sentry: {
    enabled: boolean;
    dsn: string;
    environment: string;
    includePrivateInfo: boolean;
  };
  telemetry: {
    enabled: boolean;
    includeSystemInfo: boolean;
    includeAudioInfo: boolean;
  };
}

// Realtime settings matching backend structure
export interface RealtimeSettings {
  interval?: number;
  processingTime?: boolean;
  audio?: AudioSettings;
  dashboard?: Dashboard;
  dynamicThreshold?: DynamicThresholdSettings;
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
}

// WebServer settings
export interface WebServerSettings {
  port?: string;
  log?: LogConfig;
  liveStream?: LiveStreamSettings;
}

// Dashboard settings
export interface Dashboard {
  thumbnails: Thumbnails;
  summaryLimit: number;
}

export interface Thumbnails {
  debug?: boolean;
  summary: boolean;
  recent: boolean;
  imageProvider: string;
  fallbackPolicy: string;
}

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
  listen?: string;  // e.g., "0.0.0.0:8090"
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

// Output settings
export interface OutputSettings {
  file?: {
    enabled: boolean;
    path: string;
    type: string;
  };
  sqlite?: {
    enabled: boolean;
    path: string;
  };
  mysql?: {
    enabled: boolean;
    username: string;
    password: string;
    database: string;
    host: string;
    port: string;
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
      dynamicThreshold: {
        enabled: false,
        debug: false,
        trigger: 3,
        min: 0.1,
        validHours: 24,
      },
      rangeFilter: {
        model: 'latest',
        threshold: 0.03,
        speciesCount: null,
        species: [],
      },
      database: {
        type: 'sqlite',
        path: '/data/birdnet.db',
        host: 'localhost',
        port: 3306,
        name: 'birdnet',
        username: '',
        password: '',
      },
    },
    realtime: {
      interval: 15,
      processingTime: false,
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
        },
        retention: {
          policy: 'none',
          maxAge: '7d',
          maxUsage: '80%',
          minClips: 10,
          keepSpectrograms: false,
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
      },
      species: {
        include: [],
        exclude: [],
        config: {},
      },
      weather: {
        provider: 'none',
        pollInterval: 60,
        debug: false,
        openWeather: {
          enabled: false,
          apiKey: '',
          endpoint: 'https://api.openweathermap.org/data/2.5/weather',
          units: 'metric',
          language: 'en',
        },
      },
      dashboard: {
        thumbnails: {
          summary: true,
          recent: true,
          imageProvider: 'wikimedia',
          fallbackPolicy: 'all',
        },
        summaryLimit: 100,
      },
    },
    webServer: {},
    security: {
      host: '',
      autoTls: false,
      basicAuth: {
        enabled: false,
        username: '',
        password: '',
      },
      googleAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
      githubAuth: {
        enabled: false,
        clientId: '',
        clientSecret: '',
        userId: '',
      },
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

export const speciesSettings = derived(settingsStore, $store => $store.formData.realtime?.species);

export const dashboardSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.dashboard
);

export const securitySettings = derived(settingsStore, $store => $store.formData.security);

export const sentrySettings = derived(settingsStore, $store => $store.formData.sentry);

export const rtspSettings = derived(settingsStore, $store => $store.formData.realtime?.rtsp);

export const integrationSettings = derived(settingsStore, $store => ({
  birdweather: $store.formData.realtime?.birdweather,
  mqtt: $store.formData.realtime?.mqtt,
  observability: {
    prometheus: {
      enabled: $store.formData.realtime?.telemetry?.enabled ?? false,
      port: $store.formData.realtime?.telemetry?.listen ? 
        parseInt($store.formData.realtime.telemetry.listen.split(':')[1] || '8090') : 8090,
      path: '/metrics'
    }
  },
  weather: $store.formData.realtime?.weather,
}));

export const supportSettings = derived(settingsStore, $store => ({
  sentry: $store.formData.sentry,
  telemetry: $store.formData.realtime?.telemetry,
}));

// Settings actions
export const settingsActions = {
  async loadSettings() {
    settingsStore.update(state => ({ ...state, isLoading: true, error: null }));
    try {
      const data = await settingsAPI.load();
      const mergedData = { ...createEmptySettings(), ...data };

      settingsStore.update(state => ({
        ...state,
        formData: mergedData,
        originalData: JSON.parse(JSON.stringify(mergedData)),
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
      const newSectionData = { ...(state.formData[section] || {}), ...data };
      return {
        ...state,
        formData: {
          ...state.formData,
          [section]: newSectionData,
        },
      };
    });
  },

  async saveSettings() {
    settingsStore.update(state => ({ ...state, isSaving: true, error: null }));
    try {
      const currentState = get(settingsStore);

      await settingsAPI.save(currentState.formData);

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
    settingsStore.update(state => ({
      ...state,
      formData: {
        ...state.formData,
        [section]: JSON.parse(JSON.stringify(state.originalData[section])),
      },
    }));
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
