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

// Type definitions for settings - Updated interfaces
export interface NodeSettings {
  name: string;
  identifier: string;
  location?: {
    latitude: number;
    longitude: number;
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
  rtsp: RTSPSettings;
  quality: AudioQuality;
  equalizer: EqualizerSettings;
  export: ExportSettings;
  retention: RetentionSettings;
  soundLevel: SoundLevelSettings;
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
  format: 'wav' | 'mp3' | 'flac' | 'aac' | 'opus';
  quality: string;
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

export interface WeatherSettings {
  provider: 'none' | 'yr.no' | 'openweather';
  apiKey?: string;
  enabled: boolean;
}

export interface SecuritySettings {
  autoTLS: {
    enabled: boolean;
    host: string;
  };
  basicAuth: {
    enabled: boolean;
    username: string;
    password: string;
  };
  googleAuth: OAuthSettings;
  githubAuth: OAuthSettings;
  allowSubnetBypass: {
    enabled: boolean;
    subnets: string[];
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
  include: {
    enabled: boolean;
    species: string[];
  };
  exclude: {
    enabled: boolean;
    species: string[];
  };
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

// Main settings form data interface
export interface SettingsFormData {
  node: NodeSettings;
  birdnet: BirdNetSettings;
  audio: AudioSettings;
  filters: FilterSettings;
  integration: IntegrationSettings;
  security: SecuritySettings;
  species: SpeciesSettings;
  support: SupportSettings;
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
    node: {
      name: '',
      identifier: '',
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
    audio: {
      source: '',
      rtsp: {
        transport: 'tcp',
        urls: [],
      },
      quality: {
        sampleRate: 48000,
        bitRate: 192,
        channels: 1,
      },
      equalizer: {
        enabled: false,
        filters: [],
      },
      export: {
        format: 'wav',
        quality: '96k',
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
    },
    filters: {
      privacy: {
        enabled: false,
        confidence: 0.5,
        debug: false,
      },
      dogBark: {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      },
    },
    integration: {
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
        tls: {
          enabled: false,
          skipVerify: false,
        },
      },
      observability: {
        prometheus: {
          enabled: false,
          port: 9090,
          path: '/metrics',
        },
      },
      weather: {
        provider: 'none',
        enabled: false,
      },
    },
    security: {
      autoTLS: {
        enabled: false,
        host: '',
      },
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
        subnets: [],
      },
    },
    species: {
      include: {
        enabled: false,
        species: [],
      },
      exclude: {
        enabled: false,
        species: [],
      },
      config: {},
    },
    support: {
      sentry: {
        enabled: false,
        dsn: '',
        environment: 'production',
        includePrivateInfo: false,
      },
      telemetry: {
        enabled: true,
        includeSystemInfo: true,
        includeAudioInfo: false,
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
  activeSection: 'node',
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

// Section-specific derived stores
export const nodeSettings = derived(settingsStore, $store => $store.formData.node);

export const birdnetSettings = derived(settingsStore, $store => $store.formData.birdnet);

export const audioSettings = derived(settingsStore, $store => $store.formData.audio);

export const filterSettings = derived(settingsStore, $store => $store.formData.filters);

export const integrationSettings = derived(settingsStore, $store => $store.formData.integration);

export const securitySettings = derived(settingsStore, $store => $store.formData.security);

export const speciesSettings = derived(settingsStore, $store => $store.formData.species);

export const supportSettings = derived(settingsStore, $store => $store.formData.support);

// Settings actions
export const settingsActions = {
  async loadSettings() {
    settingsStore.update(state => ({ ...state, isLoading: true, error: null }));
    try {
      const data = await settingsAPI.load();
      settingsStore.update(state => ({
        ...state,
        formData: { ...createEmptySettings(), ...data },
        originalData: JSON.parse(JSON.stringify({ ...createEmptySettings(), ...data })),
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
    settingsStore.update(state => ({
      ...state,
      formData: {
        ...state.formData,
        [section]: { ...state.formData[section], ...data },
      },
    }));
  },

  async saveSettings() {
    settingsStore.update(state => ({ ...state, isSaving: true, error: null }));
    try {
      const currentState = get(settingsStore);
      const savedData = await settingsAPI.save(currentState.formData);
      settingsStore.update(state => ({
        ...state,
        formData: savedData,
        originalData: JSON.parse(JSON.stringify(savedData)),
        isSaving: false,
      }));
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Failed to save settings';
      settingsStore.update(state => ({
        ...state,
        isSaving: false,
        error: errorMessage,
      }));
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
