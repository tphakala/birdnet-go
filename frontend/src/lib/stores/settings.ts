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
import type {
  ExportFormat,
  LosslessExportFormat,
} from '$lib/desktop/features/settings/pages/audioExportFormat';
import { t } from '$lib/i18n';
import { getLogger } from '$lib/utils/logger';
import { pruneRemovedModelAliases } from '$lib/utils/modelAliases';
import { isPlainObject, safeGet, safeSpread } from '$lib/utils/security';
import { settingsAPI } from '$lib/utils/settingsApi.js';
import type { SettingsSectionName, SettingsSectionPayloads } from '$lib/utils/settingsApi.js';
import { withoutServerOwnedKeys } from '$lib/utils/settingsSections';
import { coerceSettings } from '$lib/utils/settingsCoercion';
import {
  mergeSettingsPatch,
  mergeSettingsPatchKeepingEdits,
  rebaseSettingsKeepingEdits,
} from '$lib/utils/settingsMerge';
import { DEFAULT_REGION_MODE } from '$lib/utils/variantSelection';
import { weatherDefaults } from '$lib/utils/weatherDefaults';
import { derived, get, writable } from 'svelte/store';
import { toastActions } from './toast.js';

const logger = getLogger('settings');

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
  locationConfigured: boolean; // true when location has been explicitly configured
  /**
   * Regional model preference for the gallery: 'auto' (resolve the best regional
   * variant from the station location), 'global' (always the worldwide model), or
   * a region slug. '' and absent both mean 'auto' (the Go field is omitempty).
   */
  modelRegion?: string;
  rangeFilter: RangeFilterSettings;
  // Host used for model downloads, e.g. https://hf-mirror.com where
  // huggingface.co is unreachable. When empty the backend falls back to the
  // HF_ENDPOINT environment variable first and only then to https://huggingface.co,
  // so an empty value here does not necessarily mean the default host is in use.
  huggingFaceEndpoint?: string;
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
  passUnmappedSpecies: boolean;
  speciesCount: number | null;
  species: string[];
}

/** Species entry returned by the range filter test endpoint */
export interface RangeFilterSpeciesEntry {
  commonName?: string;
  scientificName?: string;
  label?: string;
  score?: number;
}

/** Result from loadRangeFilterSpecies */
export interface RangeFilterSpeciesResult {
  count: number;
  species: RangeFilterSpeciesEntry[];
}

export interface BatSettings {
  enabled: boolean;
  threshold: number;
  locale?: string;
  filterEnabled: boolean;
  nighttimeOnly: boolean;
  falsePositiveFilter?: {
    level: number;
  };
  ultrasonicFilter?: {
    enabled: boolean;
  };
}

// Secondary acoustic classifiers (Perch v2, BirdNET v3.0). Their detections
// follow the primary BirdNET threshold unless overrideThreshold is enabled, in
// which case `threshold` gates them instead. modelPath/labelPath/locale are
// managed by the model gallery, not the detection-settings form.
export interface PerchSettings {
  modelPath?: string | null;
  labelPath?: string | null;
  overrideThreshold: boolean;
  threshold: number;
  locale?: string;
}

export interface BirdNetV3Settings {
  modelPath?: string | null;
  labelPath?: string | null;
  overrideThreshold: boolean;
  threshold: number;
  locale?: string;
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

// AudioSourceConfig represents a single sound card audio source
export interface AudioSourceConfig {
  name: string;
  device: string;
  gain: number;
  sampleRate?: number; // capture sample rate in Hz; 0 or undefined means 48000
  models: string[]; // e.g. ["birdnet", "perch_v2"]
  equalizer?: EqualizerSettings;
  quietHours?: QuietHoursConfig;
}

export interface AudioSettings {
  sources: AudioSourceConfig[];
  source: string; // legacy single source, kept for backward compat
  ffmpegPath?: string;
  streamTransport?: string;
  export: ExportSettings;
  soundLevel: SoundLevelSettings;
  useAudioCore?: boolean;
  equalizer: EqualizerSettings;
  quietHours?: QuietHoursConfig;
}

export interface SoundLevelSettings {
  enabled: boolean;
  interval: number;
}

// Audio gain bounds in dB, shared by the sound card, stream, and export gain
// controls. Mirrors the backend MinAudioGain/MaxAudioGain validation range so
// the frontend clamp and sliders stay in sync if the range ever changes.
export const AUDIO_GAIN_MIN_DB = -40;
export const AUDIO_GAIN_MAX_DB = 40;

// Stream type constants
export const StreamTypes = {
  RTSP: 'rtsp',
  HTTP: 'http',
  HLS: 'hls',
  RTMP: 'rtmp',
  UDP: 'udp',
} as const;

export type StreamType = (typeof StreamTypes)[keyof typeof StreamTypes];

export type ChannelMode = 'downmix' | 'left' | 'right';

export type MediaMode = 'auto' | 'audio-only' | 'full-stream';

// QuietHoursConfig represents quiet hours configuration for a stream or sound card
export interface QuietHoursConfig {
  enabled: boolean;
  mode: 'fixed' | 'solar';
  startTime: string; // HH:MM format for fixed mode
  endTime: string; // HH:MM format for fixed mode
  startEvent: 'sunrise' | 'sunset'; // solar event for solar mode
  startOffset: number; // minutes offset from start event
  endEvent: 'sunrise' | 'sunset'; // solar event for solar mode
  endOffset: number; // minutes offset from end event
}

// Default quiet hours configuration
export const defaultQuietHoursConfig: QuietHoursConfig = {
  enabled: false,
  mode: 'fixed',
  startTime: '22:00',
  endTime: '06:00',
  startEvent: 'sunset',
  startOffset: 0,
  endEvent: 'sunrise',
  endOffset: 0,
};

// StreamConfig represents a single audio stream source
export interface StreamConfig {
  name: string; // Required: descriptive name like "Front Yard"
  url: string; // Required: stream URL
  enabled: boolean; // Materialized during settings coercion for backward compatibility
  type: StreamType; // Stream type: rtsp, http, hls, rtmp, udp
  transport?: 'tcp' | 'udp'; // Transport protocol (for RTSP/RTMP only)
  channelMode?: ChannelMode; // Channel selection mode: downmix, left, or right
  mediaMode?: MediaMode; // RTSP media request: auto, audio-only, or full-stream (empty = full-stream)
  models?: string[]; // Model IDs for this stream (e.g., ["birdnet", "perch_v2"])
  equalizer?: EqualizerSettings; // Per-stream EQ (undefined = use global)
  quietHours?: QuietHoursConfig; // Quiet hours configuration
  gain?: number; // Input gain in dB (0 = no adjustment)
}

// ChannelEnergy represents the energy level of a single audio channel
export interface ChannelEnergy {
  channel: number;
  label: string;
  rmsDbfs: number;
}

// ChannelAnalysis represents the result of analyzing audio channels for a stream
export interface ChannelAnalysis {
  channels: number;
  energy: ChannelEnergy[];
  recommended: ChannelMode;
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
  type: EqualizerFilterType;
  frequency: number;
  q?: number;
  width?: number; // Bandwidth in Hz (for BandReject)
  gain?: number;
  passes?: number; // Number of filter passes: 0 = off; 1 or more applies it that many times; the backend skips a negative value or one above its limit (the editor offers 0 to 4)
}

export interface ExportSettings {
  type: ExportFormat;
  ultrasonicType: LosslessExportFormat; // bat/ultrasonic captures above 48 kHz; WAV or FLAC only
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
  // Dedicated Silero VAD speech gate. Backend-managed (toggled via config/API,
  // modelPath set by the model gallery install); no dedicated UI yet. Declared
  // here so a settings save round-trip preserves it rather than stripping it.
  vad?: PrivacyFilterVadSettings;
}

export interface PrivacyFilterVadSettings {
  enabled: boolean;
  threshold: number;
  modelPath: string;
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

export interface DaylightFilterSettings {
  enabled: boolean;
  debug: boolean;
  offset: number;
  species: string[];
}

export interface EBirdSettings {
  enabled: boolean;
  apiKey: string;
  cacheTTL: number; // cache time-to-live in hours (default: 24)
  locale: string; // locale for eBird data (e.g., "en", "es")
}

export interface IntegrationSettings {
  birdweather: BirdWeatherSettings;
  mqtt: MQTTSettings;
  observability: ObservabilitySettings;
  weather: WeatherSettings;
  ebird: EBirdSettings;
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
    insecureSkipVerify: boolean;
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

export interface PirateWeatherSettings {
  apiKey: string;
  endpoint: string;
}

export interface WeatherSettings {
  provider: 'none' | 'yrno' | 'openweather' | 'wunderground' | 'pirateweather';
  pollInterval: number;
  debug: boolean;
  openWeather: OpenWeatherSettings;
  wunderground: WundergroundSettings;
  pirateWeather: PirateWeatherSettings;
}

// New array-based OAuth provider configuration
export interface OAuthProviderConfig {
  provider: 'google' | 'github' | 'microsoft' | 'line' | 'kakao' | 'oidc';
  enabled: boolean;
  clientId: string;
  clientSecret: string;
  redirectUri?: string;
  userId?: string;
  issuerUrl?: string; // OIDC only: IdP discovery URL
  scopes?: string[]; // OIDC only: custom scopes (defaults: openid, profile, email)
}

export interface SecuritySettings {
  baseUrl: string;
  host: string;
  autoTls: boolean;
  tlsMode: string; // "" | "autotls" | "manual" | "selfsigned"
  tlsPort: string; // port for HTTPS (default: "8443")
  selfSignedValidity: string;
  redirectToHttps: boolean;
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
  publicAccess?: {
    liveAudio: boolean;
  };
  privateMode?: boolean;
  /**
   * Reverse proxies whose forwarded client-IP headers are honored for
   * authentication decisions such as the subnet bypass (security.trustedProxies).
   * Each entry is an IPv4 or IPv6 address, a CIDR, or the reserved `cloudflare`
   * preset token. Coercion turns a missing or null value into `[]`.
   */
  trustedProxies?: string[];
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

// Infrequent tracking settings
export interface InfrequentTrackingSettings {
  enabled: boolean;
  absenceDays: number; // Days since last detection before a return is flagged "infrequent"
}

// Species tracking settings
export interface SpeciesTrackingSettings {
  enabled: boolean;
  newSpeciesWindowDays: number; // Days to consider a species "new"
  syncIntervalMinutes: number; // Interval to sync with database
  notificationSuppressionHours: number; // Hours to suppress duplicate notifications
  yearlyTracking: YearlyTrackingSettings;
  seasonalTracking: SeasonalTrackingSettings;
  infrequentTracking: InfrequentTrackingSettings;
}

// Extended capture settings
export interface ExtendedCaptureSettings {
  enabled: boolean;
  maxDuration: number;
  captureBufferSeconds: number;
  species: string[];
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
  daylightFilter?: DaylightFilterSettings;
  rtsp?: RTSPSettings;
  mqtt?: MQTTSettings;
  telemetry?: TelemetrySettings;
  monitoring?: MonitoringSettings;
  species?: SpeciesSettings;
  weather?: WeatherSettings;
  speciesTracking?: SpeciesTrackingSettings;
  extendedCapture?: ExtendedCaptureSettings;
  ebird?: EBirdSettings;
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
  colorScheme?: string; // Color scheme: "blue", "forest", "amber", "violet", "rose", "custom"
  customColors?: { primary: string; accent: string }; // Custom scheme hex colors
  logoStyle?: string; // Logo display style: "gradient" or "solid"
  layout?: DashboardLayout; // Configurable dashboard element layout
  defaultAudioGain?: number; // Default playback gain in dB (0-24)
}

// Dashboard layout configuration
export interface DashboardLayout {
  elements: DashboardElement[];
}

// Dashboard element types
export type DashboardElementType =
  | 'banner'
  | 'daily-summary'
  | 'new-species-highlights'
  | 'currently-hearing'
  | 'detections-grid'
  | 'live-spectrogram'
  | 'video-embed';

// A single configurable element on the dashboard
export interface DashboardElement {
  id?: string; // unique identifier (e.g., "daily-summary-0"); stable across reorders
  type: DashboardElementType;
  enabled: boolean;
  width?: 'full' | 'half'; // card width; defaults to "full" if omitted
  banner?: BannerConfig;
  video?: VideoEmbedConfig;
  summary?: DailySummaryConfig;
  grid?: DetectionsGridConfig;
}

// Banner element configuration
export interface BannerConfig {
  showImage: boolean;
  imagePath: string;
  title: string;
  description: string;
  showLocationMap: boolean;
  showWeather: boolean;
  mapZoom?: number;
  showPin?: boolean;
  mapExpandable?: boolean;
  timeFormat?: '12h' | '24h';
}

// Video embed element configuration
export interface VideoEmbedConfig {
  url: string;
  title: string;
}

// Daily summary element configuration
export interface DailySummaryConfig {
  summaryLimit: number;
}

// Detections grid element configuration (placeholder for future card display options)
export type DetectionsGridConfig = Record<string, never>;

export interface Thumbnails {
  debug?: boolean;
  summary: boolean;
  recent: boolean;
  imageProvider: string;
  fallbackPolicy: string;
}

// Spectrogram size options
export type SpectrogramSize = 'md' | 'lg' | 'xl';

// Spectrogram generation mode options
export type SpectrogramMode = 'auto' | 'prerender' | 'user-requested';

// Spectrogram style preset options
export type SpectrogramStyle = 'default' | 'scientific_dark' | 'high_contrast_dark' | 'scientific';

// Spectrogram dynamic range preset options (dynamic range in dB)
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
  size: SpectrogramSize; // Default size for all modes (md=514px, lg=1026px, xl=2050px)
  raw: boolean; // Generate raw spectrogram without axes/legend (default: true)
  style?: SpectrogramStyle; // Visual style preset (default: 'default')
  dynamicRange?: SpectrogramDynamicRange; // Dynamic range in dB: 80 (high contrast), 100 (standard), 120 (extended)
}

// Default spectrogram settings
export const DEFAULT_SPECTROGRAM_SETTINGS: SpectrogramPreRender = {
  mode: 'auto',
  enabled: false,
  size: 'md',
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
  bat?: BatSettings;
  perch?: PerchSettings;
  birdnetv3?: BirdNetV3Settings;
  input?: unknown; // Not exposed via JSON
  realtime?: RealtimeSettings;
  webServer?: WebServerSettings;
  security?: SecuritySettings;
  sentry?: SentrySettings;
  output?: OutputSettings;
  backup?: BackupSettings;
  notification?: NotificationSettings;
  taxonomySynonyms?: Record<string, string>;
}

// Global settings state interface
export interface GlobalSettingsState {
  formData: SettingsFormData;
  originalData: SettingsFormData;
  isLoading: boolean;
  isSaving: boolean;
  activeSection: string;
  error: string | null;
  /**
   * True when the failure in `error` was also shown as a toast, so the Settings page
   * banner does not announce it a second time. Written with every non-null `error`;
   * absent means no toast.
   */
  errorToasted?: boolean;
  dataLoaded: boolean;
}

/** Options for {@link settingsActions.saveSettings}. */
export interface SaveSettingsOptions {
  /** Show the success and failure toasts. Defaults to true. */
  notify?: boolean;
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
function createEmptySettings(): SettingsFormData & { security: SecuritySettings } {
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
      locationConfigured: false,
      modelRegion: DEFAULT_REGION_MODE,
      rangeFilter: {
        threshold: 0.03,
        passUnmappedSpecies: false,
        speciesCount: null,
        species: [],
      },
      huggingFaceEndpoint: '',
    },
    bat: {
      enabled: false,
      threshold: 0.5,
      filterEnabled: false,
      nighttimeOnly: true,
      ultrasonicFilter: {
        enabled: true,
      },
    },
    perch: {
      overrideThreshold: false,
      threshold: 0.5,
    },
    birdnetv3: {
      overrideThreshold: false,
      threshold: 0.5,
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
        sources: [],
        source: '',
        ffmpegPath: '',
        streamTransport: 'auto',
        export: {
          type: 'wav',
          ultrasonicType: 'flac',
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
        confidence: 0.05,
        debug: false,
        vad: {
          enabled: false,
          threshold: 0.6,
          modelPath: '',
        },
      },
      dogBarkFilter: {
        enabled: false,
        confidence: 0.5,
        remember: 30,
        debug: false,
        species: [],
      },
      daylightFilter: {
        enabled: false,
        debug: false,
        offset: 0,
        species: [],
      },
      extendedCapture: {
        enabled: false,
        maxDuration: 120,
        captureBufferSeconds: 0,
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
          insecureSkipVerify: false,
        },
        homeAssistant: {
          enabled: false,
          discoveryPrefix: 'homeassistant',
          deviceName: 'BirdNET-Go',
        },
      },
      ebird: {
        enabled: false,
        apiKey: '',
        cacheTTL: 24,
        locale: 'en',
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
          imageProvider: 'avicommons',
          fallbackPolicy: 'none',
        },
        summaryLimit: 30,
        spectrogram: DEFAULT_SPECTROGRAM_SETTINGS,
        temperatureUnit: 'celsius',
        layout: {
          elements: [
            {
              id: 'daily-summary-0',
              type: 'daily-summary',
              enabled: true,
              summary: { summaryLimit: 30 },
            },
            { id: 'currently-hearing-0', type: 'currently-hearing', enabled: true },
            { id: 'live-spectrogram-0', type: 'live-spectrogram', enabled: true },
            { id: 'detections-grid-0', type: 'detections-grid', enabled: true },
          ],
        },
        defaultAudioGain: 0,
      },
    },
    webServer: {},
    security: {
      baseUrl: '',
      host: '',
      autoTls: false,
      tlsMode: '',
      tlsPort: '8443',
      selfSignedValidity: '1825d',
      redirectToHttps: false,
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
      trustedProxies: [],
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
    taxonomySynonyms: {},
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
  dataLoaded: false,
};

export const settingsStore = writable<GlobalSettingsState>(initialState);

// Derived stores for easy component usage
export const hasUnsavedChanges = derived(
  settingsStore,
  $store => JSON.stringify($store.formData) !== JSON.stringify($store.originalData)
);

// Validation errors store - pages can add errors to prevent saving
export const settingsValidationErrors = writable<string[]>([]);

/**
 * Optional translated, human-readable reasons for entries of settingsValidationErrors,
 * keyed by the entry. The save bar lists the reason of every active entry that has
 * one, so a page can say which tab and field blocks saving. Pages that set a reason
 * remove it together with the entry.
 */
export const settingsValidationReasons = writable<ReadonlyMap<string, string>>(new Map());

/**
 * Replaces the validation entries a page owns. `ownedKeys` are all the keys the page
 * may set; `active` maps the ones that apply now to their reason (entries of `active`
 * outside `ownedKeys` are ignored). Both stores are
 * written together, and neither is written when it would not change, so a page can
 * call this from an effect without waking the save bar. Pass an empty map to clear.
 */
export function setValidationIssues(
  ownedKeys: readonly string[],
  active: ReadonlyMap<string, string>
): void {
  // Keys the caller does not own are ignored, so they can neither leak into the
  // stores nor be listed twice.
  const owned = [...active].filter(([key]) => ownedKeys.includes(key));
  const errors = get(settingsValidationErrors);
  const nextErrors = [
    ...errors.filter(key => !ownedKeys.includes(key)),
    ...owned.map(([key]) => key),
  ];
  if (nextErrors.length !== errors.length || !nextErrors.every(key => errors.includes(key))) {
    settingsValidationErrors.set(nextErrors);
  }

  const reasons = get(settingsValidationReasons);
  const nextReasons = new Map(reasons);
  for (const key of ownedKeys) nextReasons.delete(key);
  for (const [key, reason] of owned) nextReasons.set(key, reason);
  const reasonsUnchanged =
    nextReasons.size === reasons.size &&
    [...nextReasons].every(([key, reason]) => reasons.get(key) === reason);
  if (!reasonsUnchanged) settingsValidationReasons.set(nextReasons);
}

export const currentSection = derived(settingsStore, $store => $store.activeSection);

export const isLoading = derived(settingsStore, $store => $store.isLoading);

export const isSaving = derived(settingsStore, $store => $store.isSaving);

export const settingsDataLoaded = derived(settingsStore, $store => $store.dataLoaded);

// Section-specific derived stores matching backend structure
export const mainSettings = derived(settingsStore, $store => $store.formData.main);

export const birdnetSettings = derived(settingsStore, $store => $store.formData.birdnet);

export const batSettings = derived(settingsStore, $store => $store.formData.bat);

export const perchSettings = derived(settingsStore, $store => $store.formData.perch);

export const birdNetV3Settings = derived(settingsStore, $store => $store.formData.birdnetv3);

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

export const daylightFilterSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.daylightFilter
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

export const dashboardLayout = derived(
  settingsStore,
  $store => $store.formData.realtime?.dashboard?.layout
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
  ebird: $store.formData.realtime?.ebird ?? {
    enabled: false,
    apiKey: '',
    cacheTTL: 24,
    locale: 'en',
  },
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

// Extended capture settings derived store
export const extendedCaptureSettings = derived(
  settingsStore,
  $store => $store.formData.realtime?.extendedCapture
);

/** A top-level store key, or a key of the realtime object. */
type SectionStorePath =
  readonly [keyof SettingsFormData] | readonly ['realtime', keyof RealtimeSettings];

/**
 * Where each backend section handled by settingsActions.saveSection lives in
 * the store: a top-level key, or a key of the realtime object.
 */
export const SECTION_STORE_PATHS = {
  birdnet: ['birdnet'],
  sentry: ['sentry'],
  dashboard: ['realtime', 'dashboard'],
  audio: ['realtime', 'audio'],
  rtsp: ['realtime', 'rtsp'],
  privacyfilter: ['realtime', 'privacyFilter'],
  birdweather: ['realtime', 'birdweather'],
} as const satisfies Record<SettingsSectionName, SectionStorePath>;

/**
 * Section saves whose request or store merge has not finished. saveSettings
 * waits for them so its snapshot of formData never predates a merge.
 */
const inFlightSectionSaves = new Set<Promise<void>>();

/**
 * The full save that is waiting for section saves to finish, if any. A saveSettings
 * call made in that window joins it. It is cleared right before the snapshot is taken,
 * so a call made after that starts its own save.
 */
let pendingFullSave: { done: Promise<void>; notify: boolean } | null = null;

let serverSyncTail: Promise<void> = Promise.resolve();
const ignoreOutcome = () => undefined;

/**
 * Runs task after every earlier queued task has settled, so a refresh or a load
 * applies wholly before or wholly after a full save and a GET issued before a
 * PUT can never land after it. Orders loads, rebases and full saves only;
 * section saves are not queued.
 */
function runSerialized<T>(task: () => Promise<T>): Promise<T> {
  const result = serverSyncTail.then(task);
  serverSyncTail = result.then(ignoreOutcome, ignoreOutcome);
  return result;
}

/** Applies the per-section coercion to every object section. Keys without a coercion rule pass through. */
function coerceAllSections(data: SettingsFormData): SettingsFormData {
  const coerced = { ...data };
  for (const [section, sectionData] of Object.entries(data)) {
    if (sectionData && typeof sectionData === 'object') {
      // eslint-disable-next-line security/detect-object-injection -- Safe: section from Object.entries of known object
      (coerced as unknown as Record<string, unknown>)[section] = coerceSettings(
        section,
        sectionData as Record<string, unknown>
      );
    }
  }
  return coerced;
}

/**
 * Spreads a settings response over the defaults and coerces each object section.
 * Keys the interfaces do not declare (for example models) are kept as received.
 */
function normalizeLoadedSettings(data: SettingsFormData): SettingsFormData {
  return coerceAllSections({ ...createEmptySettings(), ...data });
}

/** HTTP status the backend answers a stale If-Match with. */
const HTTP_PRECONDITION_FAILED = 412;

/** Tries a full save makes with If-Match: one send plus one resend after a 412 and a rebase. */
const MAX_SETTINGS_SAVE_ATTEMPTS = 2;

/** Prefix of a weak entity tag; the backend compares If-Match strongly, so a weak tag never matches. */
const WEAK_ETAG_PREFIX = 'W/';

/**
 * Set once a save sees two 412s in a row name the same current ETag. That means the
 * validator is rewritten between the browser and the server (a compressing reverse
 * proxy such as Caddy or Apache mod_deflate appends "-gzip"), so no If-Match from a
 * GET can ever match. From then on saves in this page load send no If-Match, which
 * is how saves worked before conditional saves. Reset by a page reload.
 */
let ifMatchUnusable = false;

/** Test-only: forget a detected ETag rewrite so each test starts as a fresh page. */
export function resetIfMatchFallbackForTest(): void {
  ifMatchUnusable = false;
}

/**
 * Thrown when the save is still refused with a 412 after its attempts: the settings keep
 * changing under the form. A save makes up to three sends, the third without If-Match
 * once the validator is judged rewritten in transit. Callers identify the conflict with
 * `instanceof`.
 */
export class SettingsChangedElsewhereError extends Error {
  /** The status of the conflict it reports, so telemetry filters it like the 412 itself. */
  readonly status = HTTP_PRECONDITION_FAILED;

  constructor() {
    super(t('settings.errors.changedElsewhere'));
    this.name = 'SettingsChangedElsewhereError';
  }
}

/**
 * True for a 412 from the backend. Duck-typed on status: the store does not
 * import the api module's ApiError, and tests mock settingsApi only.
 */
function isPreconditionFailed(error: unknown): boolean {
  return (
    typeof error === 'object' &&
    error !== null &&
    'status' in error &&
    error.status === HTTP_PRECONDITION_FAILED
  );
}

/**
 * The If-Match value to send for an ETag from the pre-save GET: only a strong
 * ETag. Absent (an older backend) or weak (a proxy weakened it) means no
 * protection, which is how saves worked before If-Match, rather than a save
 * that can never match. Also absent once a rewritten ETag was detected.
 */
function usableIfMatch(etag: string | null): string | undefined {
  if (ifMatchUnusable) return undefined;
  return etag && !etag.startsWith(WEAK_ETAG_PREFIX) ? etag : undefined;
}

/**
 * Fetches the current server settings, normalized like a load, with the ETag
 * the response carried (null when absent). Throws on a malformed response.
 */
async function fetchServerSnapshot(): Promise<{
  snapshot: SettingsFormData;
  etag: string | null;
}> {
  let etag: string | null = null;
  const data = await settingsAPI.load({
    onETag: value => {
      etag = value;
    },
  });
  if (!isPlainObject(data)) {
    throw new Error(t('settings.errors.loadFailed'));
  }
  return { snapshot: normalizeLoadedSettings(data), etag };
}

/**
 * Rebases the state on a fresh server snapshot: pending edits are kept, every
 * other path takes the server value, and originalData becomes the snapshot. An
 * edit wins over a server change to the same path (last writer wins, see
 * rebaseSettingsKeepingEdits).
 */
function rebaseState(state: GlobalSettingsState, fresh: SettingsFormData): GlobalSettingsState {
  const merged = rebaseSettingsKeepingEdits(
    { ...state.formData },
    { ...state.originalData },
    { ...fresh }
  );
  const formData = pruneRemovedModelAliases({ ...fresh, ...merged }, state.originalData, fresh);
  return { ...state, formData, originalData: JSON.parse(JSON.stringify(fresh)) };
}

/**
 * Sends the whole form with PUT, with If-Match when ifMatch is set. Resolves
 * { conflict: false } when the server accepted it and, on a 412, { conflict: true }
 * with the current ETag the rejection named (null when it named none, or when no
 * If-Match was sent). Rethrows every other failure.
 */
async function sendSettings(
  data: SettingsFormData,
  ifMatch: string | undefined
): Promise<{ conflict: false } | { conflict: true; currentETag: string | null }> {
  let currentETag: string | null = null;
  try {
    // Only pass options when there is an If-Match, so a bare save() call stays as before.
    await (ifMatch
      ? settingsAPI.save(data, {
          ifMatch,
          onETag: value => {
            currentETag = value;
          },
        })
      : settingsAPI.save(data));
    return { conflict: false };
  } catch (error) {
    if (!isPreconditionFailed(error)) throw error;
    return { conflict: true, currentETag };
  }
}

/**
 * Rebases the store on the current server state, then sends the whole form to
 * the backend and marks exactly what was sent as saved. The PUT carries the
 * snapshot's strong ETag as If-Match; on a 412 (the settings changed between the
 * GET and the PUT) the rebase and send run once more on a fresh snapshot, and a
 * second 412 fails the save with SettingsChangedElsewhereError, edits kept.
 * Unless both 412s named the same non-empty current ETag: then nothing changed on the
 * server and the ETag is rewritten on the way, so one more send without If-Match
 * follows and later saves in this page load send none.
 * Resets isSaving and rethrows on every failure path, including the refusal
 * before a load. A failed fetch of the server state fails the save before any
 * request is sent.
 */
async function persistFormData(notify: boolean): Promise<void> {
  try {
    if (!get(settingsStore).dataLoaded) {
      logger.warn('Refusing to save settings before they have been loaded');
      throw new Error(t('settings.errors.loadFailed'));
    }
    settingsStore.update(state => ({ ...state, isSaving: true, error: null }));

    await runSerialized(async () => {
      // The current ETag the previous attempt's 412 named, to spot a rewritten validator.
      let previousConflictETag: string | null = null;
      for (let attempt = 1; ; attempt++) {
        // Rebase first so a change made on the server since the load (a model
        // gallery action) is not overwritten by load-time values.
        const { snapshot, etag } = await fetchServerSnapshot();
        settingsStore.update(state => rebaseState(state, snapshot));
        const rebased = get(settingsStore);
        const sent: SettingsFormData = JSON.parse(JSON.stringify(rebased.formData));
        const loadedLocale = rebased.originalData.realtime?.dashboard?.locale;

        const coercedFormData = coerceAllSections(sent);

        // Convert empty strings to null for modelPath and labelPath to signal "revert to default"
        // This ensures the config file is properly cleaned when users clear these fields
        // Trim whitespace to handle cases like "   " which should also be treated as empty
        if (coercedFormData.birdnet.modelPath?.trim() === '') {
          coercedFormData.birdnet.modelPath = null;
        }
        if (coercedFormData.birdnet.labelPath?.trim() === '') {
          coercedFormData.birdnet.labelPath = null;
        }

        const ifMatch = usableIfMatch(etag);
        const outcome = await sendSettings(coercedFormData, ifMatch);
        if (outcome.conflict) {
          if (attempt < MAX_SETTINGS_SAVE_ATTEMPTS) {
            previousConflictETag = outcome.currentETag;
            continue;
          }
          // Both GET-issued ETags were refused while the server named the same current
          // ETag twice: nothing changed between the two PUTs, so the validator is
          // rewritten on the way. Save once more without If-Match and stop sending it.
          if (outcome.currentETag && outcome.currentETag === previousConflictETag) {
            ifMatchUnusable = true;
            logger.warn(
              'Settings If-Match is rewritten between the browser and the server; saving without it for this page load'
            );
            continue;
          }
          throw new SettingsChangedElsewhereError();
        }

        // Read newLocale from coercedFormData (the value we actually persisted)
        // and compare to originalData (the snapshot rebased on the backend).
        await afterSettingsPersisted(coercedFormData.realtime?.dashboard?.locale, loadedLocale);

        // Mark what was sent as saved, not formData as it is now, so an edit typed
        // while the request was in flight stays unsaved. Restart-required state is
        // owned by the backend (refreshed in afterSettingsPersisted) and surfaced
        // by the global RestartBanner, so it is not tracked client-side.
        settingsStore.update(state => ({ ...state, originalData: sent, isSaving: false }));
        return;
      }
    });

    if (notify) {
      toastActions.success(t('notifications.content.settings.savedSuccessfully'));
    }
  } catch (error) {
    const errorMessage = error instanceof Error ? error.message : t('settings.errors.saveFailed');
    settingsStore.update(state => ({
      ...state,
      isSaving: false,
      error: errorMessage,
      errorToasted: notify,
    }));

    if (notify) {
      toastActions.error(
        error instanceof SettingsChangedElsewhereError
          ? error.message
          : t('notifications.content.settings.saveFailed')
      );
    }

    throw error;
  }
}

/** Returns value when it is a plain object, otherwise undefined. */
function asRecord(value: unknown): Record<string, unknown> | undefined {
  return isPlainObject(value) ? value : undefined;
}

/**
 * Applies a section partial to both copies of the settings: a plain merge into
 * originalData and a merge into formData that keeps pending edits. Both results
 * go through the same coercion as updateSection. Inputs are not mutated.
 */
function applySectionPatch(
  state: GlobalSettingsState,
  section: SettingsSectionName,
  partial: Record<string, unknown>
): Pick<GlobalSettingsState, 'formData' | 'originalData'> {
  const path: SectionStorePath =
    // eslint-disable-next-line security/detect-object-injection -- section is a key of SECTION_STORE_PATHS by type
    SECTION_STORE_PATHS[section];
  const topKey = path[0];
  const wrapped: Record<string, unknown> = path.length === 2 ? { [path[1]]: partial } : partial;

  const savedSection = asRecord(safeGet(state.originalData, topKey));
  const formSection = asRecord(safeGet(state.formData, topKey));

  const mergedOriginal = coerceSettings(topKey, mergeSettingsPatch(savedSection, wrapped));
  const mergedForm = coerceSettings(
    topKey,
    mergeSettingsPatchKeepingEdits(formSection, savedSection, wrapped)
  );

  return {
    originalData: { ...state.originalData, [topKey]: mergedOriginal },
    formData: { ...state.formData, [topKey]: mergedForm },
  };
}

/**
 * Side effects shared by saveSettings and saveSection once the server accepted
 * a save. Refreshes restart-required status without awaiting it: the
 * RestartBanner reacts to the store whenever it lands, and a failure here must
 * not mask or delay a successful save (the banner may show stale state until
 * the next page load). Applies the UI locale, logging rather than throwing on
 * failure for the same reason, only when the saved locale differs
 * from the one previously saved, so a locale chosen via the sidebar
 * LanguageSelector (which updates localStorage but not the backend) is not
 * clobbered by whatever stale value the backend still holds.
 */
async function afterSettingsPersisted(
  newLocale: string | undefined,
  origLocale: string | undefined
): Promise<void> {
  void import('$lib/stores/restart.svelte')
    .then(({ fetchRestartStatus }) => fetchRestartStatus())
    .catch(e => {
      logger.error('Failed to refresh restart status after settings save:', e);
    });

  if (newLocale && newLocale !== origLocale) {
    try {
      // Dynamically import i18n functions to avoid circular dependencies
      const { isValidLocale, setLocale } = await import('$lib/i18n/index.js');
      if (isValidLocale(newLocale)) {
        setLocale(newLocale);
      }
    } catch (e) {
      // The save already succeeded; a failed locale switch must not report it as failed.
      logger.error('Failed to apply the UI locale after settings save:', e);
    }
  }
}

// Settings actions
export const settingsActions = {
  async loadSettings() {
    settingsStore.update(state => ({ ...state, isLoading: true, error: null }));
    try {
      // Queued with full saves and refreshes, so a load never lands between a
      // save's rebase and the originalData write that ends it.
      await runSerialized(async () => {
        const data = await settingsAPI.load();
        const coercedData = normalizeLoadedSettings(data);

        settingsStore.update(state => ({
          ...state,
          formData: coercedData,
          originalData: JSON.parse(JSON.stringify(coercedData)),
          isLoading: false,
          dataLoaded: true,
        }));
      });
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : t('settings.errors.loadFailed');
      settingsStore.update(state => ({
        ...state,
        isLoading: false,
        error: errorMessage,
        errorToasted: false,
      }));
      throw error;
    }
  },

  /**
   * Rebases the store on the current server settings without losing unsaved
   * edits. For actions the server persists outside the form (the model
   * gallery): unedited paths take the server value, edited paths keep the form
   * value. Returns at once before a load has succeeded. Does not touch
   * isLoading, isSaving or error, and rejects on failure so the caller decides
   * how to report it. Runs in order with full saves.
   */
  async refreshKeepingEdits(): Promise<void> {
    if (!get(settingsStore).dataLoaded) return;
    await runSerialized(async () => {
      const { snapshot } = await fetchServerSnapshot();
      settingsStore.update(state => rebaseState(state, snapshot));
    });
  },

  updateSection<K extends keyof SettingsFormData>(section: K, data: Partial<SettingsFormData[K]>) {
    settingsStore.update(state => {
      const currentSectionData = safeGet<SettingsFormData, K>(state.formData, section);
      const mergedData = safeSpread(currentSectionData, data) as SettingsFormData[K];

      // Apply coercion immediately to ensure values are always within valid ranges
      // This is especially important for NumberField components that need instant validation
      const coercedData = coerceSettings(
        section,
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

  /**
   * Persists one backend section with PATCH /api/v2/settings/:section and, only
   * after the server accepted it, merges the same partial into the store at that
   * section's path: a plain merge into originalData, and into formData a merge
   * that keeps any pending edit to a key the partial also sets, so unrelated and
   * conflicting unsaved edits survive and unsaved-changes tracking is unchanged.
   * Objects merge key by key and arrays replace, as on the server. A failed
   * request throws and leaves the store untouched. Shows no toast and does not
   * touch isSaving or error: the caller owns busy and error UI. Refuses before
   * settings have loaded, like saveSettings. A concurrent saveSettings waits for
   * this save to finish before it snapshots formData.
   */
  async saveSection<S extends SettingsSectionName>(
    section: S,
    partial: SettingsSectionPayloads[S]
  ): Promise<void> {
    if (!get(settingsStore).dataLoaded) {
      logger.warn('Refusing to save a settings section before settings have been loaded');
      throw new Error(t('settings.errors.loadFailed'));
    }

    const op = (async () => {
      const response = await settingsAPI.patchSection(section, partial);
      const skipped = response?.skippedFields;
      if (skipped && skipped.length > 0) {
        logger.warn('Settings section save skipped fields:', section, skipped.join(', '));
      }

      const patch = withoutServerOwnedKeys(section, partial);
      const origLocale = get(settingsStore).originalData.realtime?.dashboard?.locale;
      settingsStore.update(state => ({ ...state, ...applySectionPatch(state, section, patch) }));

      const newLocale = section === 'dashboard' ? patch.locale : undefined;
      await afterSettingsPersisted(
        typeof newLocale === 'string' ? newLocale : undefined,
        origLocale
      );
    })();

    inFlightSectionSaves.add(op);
    try {
      await op;
    } finally {
      inFlightSectionSaves.delete(op);
    }
  },

  /**
   * Sync the TLS mode after a certificate operation (upload, generate, delete)
   * that the backend has ALREADY persisted to disk. Only tlsMode and autoTls are
   * updated, and they are updated in BOTH formData and originalData so the change
   * does not register as an unsaved edit. Crucially, every other field in the
   * security section is left untouched, so unsaved edits the user made elsewhere
   * in the Security form (for example a typed-but-unsaved Basic Auth password)
   * are preserved. This replaces a full loadSettings() reload, which overwrote
   * the entire store and discarded those unsaved edits.
   */
  syncTLSMode(mode: string) {
    settingsStore.update(state => {
      const autoTls = mode === 'autotls';
      // Fall back to the store's own defaults (not a bare {}) if a security
      // section is somehow absent, so required fields are never stripped. In
      // practice this never fires: the cert handlers that call this only run
      // after settings have loaded, so security is always populated.
      const sync = (security?: SecuritySettings): SecuritySettings => ({
        ...(security ?? createEmptySettings().security),
        tlsMode: mode,
        autoTls,
      });
      return {
        ...state,
        formData: { ...state.formData, security: sync(state.formData.security) },
        originalData: { ...state.originalData, security: sync(state.originalData.security) },
      };
    });
  },

  updateTaxonomySynonyms(synonyms: Record<string, string>) {
    settingsStore.update(state => ({
      ...state,
      formData: { ...state.formData, taxonomySynonyms: synonyms },
    }));
  },

  /**
   * Saves the whole formData to the backend. Refuses (throws before any network
   * call) while no settings load has succeeded, because formData would then hold
   * empty defaults that the backend deep-merges over the real configuration.
   * It first fetches the server settings and merges the unsaved edits onto them,
   * so changes made on the server since the load (a model gallery install) are
   * not reverted; a failed fetch fails the save with no PUT, and every full save
   * costs one extra GET. Pass `{ notify: false }` to suppress the success and failure toasts.
   *
   * A call made while an earlier call still waits for section saves joins it:
   * one request, one result, and the success or failure toast if any joined call
   * asked for it.
   */
  async saveSettings(options: SaveSettingsOptions = {}) {
    const { notify = true } = options;

    if (pendingFullSave) {
      pendingFullSave.notify ||= notify;
      return pendingFullSave.done;
    }
    if (inFlightSectionSaves.size === 0) return persistFormData(notify);

    // Let section saves finish first (success or failure, request and store
    // merge) so the snapshot taken in persistFormData carries what they patched.
    // The pending save is published before anything else runs so that a call
    // made from here on joins it instead of sending a second request.
    const pending = { notify, done: Promise.resolve() };
    pendingFullSave = pending;
    settingsStore.update(state => ({ ...state, isSaving: true, error: null }));
    pending.done = (async () => {
      try {
        // Loop, because a section save can start while an earlier one is awaited.
        while (inFlightSectionSaves.size > 0) {
          await Promise.allSettled([...inFlightSectionSaves]);
        }
      } finally {
        pendingFullSave = null;
      }
      await persistFormData(pending.notify);
    })();
    return pending.done;
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

  /**
   * Load range filter species using the test endpoint that respects the
   * current threshold. Returns the filtered species list and count.
   *
   * This uses POST /api/v2/range/species/test (not GET /api/v2/range/species/list)
   * because the list endpoint ignores query parameters and returns all species,
   * which would reset the displayed count. See #2393.
   */
  async loadRangeFilterSpecies(): Promise<RangeFilterSpeciesResult> {
    const state = get(settingsStore);
    const birdnet = state.formData.birdnet;

    const data = await settingsAPI.rangeFilter.testSpecies(
      birdnet.latitude,
      birdnet.longitude,
      birdnet.rangeFilter.threshold
    );

    return {
      count: data.count,
      species: data.species ?? [],
    };
  },
};
