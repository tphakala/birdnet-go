/**
 * Settings Coercion and Validation Utilities
 *
 * Handles type coercion, range validation, and default values for settings.
 * Ensures settings values are always within valid ranges and of correct types.
 */

import type {
  BirdNetSettings,
  AudioSettings,
  SecuritySettings,
  SpeciesSettings,
  SpeciesConfig,
  Action,
  MQTTSettings,
  OAuthSettings,
  EqualizerFilter,
  NotificationSettings,
  PushSettings,
  PushProviderConfig,
  WebhookEndpointConfig,
  WebhookAuthConfig,
  PushFilterConfig,
  FalsePositiveFilterSettings,
} from '$lib/stores/settings';

// Type for partial/unknown settings data
type UnknownSettings = Record<string, unknown>;
type PartialBirdNetSettings = Partial<BirdNetSettings> & UnknownSettings;
type PartialAudioSettings = Partial<AudioSettings> & UnknownSettings;
type PartialSecuritySettings = Partial<SecuritySettings> & UnknownSettings;
type PartialSpeciesSettings = Partial<SpeciesSettings> & UnknownSettings;
type PartialMQTTSettings = Partial<MQTTSettings> & UnknownSettings;
type PartialNotificationSettings = Partial<NotificationSettings> & UnknownSettings;
type PartialFalsePositiveFilterSettings = Partial<FalsePositiveFilterSettings> & UnknownSettings;

/**
 * Coerce a value to a number within specified bounds
 */
export function coerceNumber(
  value: unknown,
  min: number,
  max: number,
  defaultValue: number
): number {
  // Handle null/undefined
  if (value === null || value === undefined) {
    return defaultValue;
  }

  // Try to convert to number
  let num: number;

  if (typeof value === 'string') {
    // Special case for string booleans - convert first, then clamp
    if (value === 'true' || value === '1') {
      num = 1;
    } else if (value === 'false' || value === '0') {
      num = 0;
    } else {
      num = parseFloat(value);
    }
  } else if (typeof value === 'boolean') {
    num = value ? 1 : 0;
  } else if (typeof value === 'number') {
    num = value;
  } else {
    return defaultValue;
  }

  // Handle NaN, Infinity
  if (!isFinite(num)) {
    return defaultValue;
  }

  // Constrain to bounds
  return Math.min(Math.max(num, min), max);
}

/**
 * Coerce a value to a boolean
 */
export function coerceBoolean(value: unknown, defaultValue = false): boolean {
  if (value === null || value === undefined) {
    return defaultValue;
  }

  if (typeof value === 'boolean') {
    return value;
  }

  if (typeof value === 'string') {
    const lower = value.toLowerCase();
    if (lower === 'true' || lower === '1' || lower === 'yes' || lower === 'on') {
      return true;
    }
    if (lower === 'false' || lower === '0' || lower === 'no' || lower === 'off' || lower === '') {
      return false;
    }
  }

  if (typeof value === 'number') {
    return value !== 0;
  }

  return defaultValue;
}

/**
 * Coerce a value to a string
 */
export function coerceString(value: unknown, defaultValue = ''): string {
  if (value === null || value === undefined) {
    return defaultValue;
  }

  if (typeof value === 'string') {
    return value;
  }

  return String(value);
}

/**
 * Coerce a value to an array
 */
export function coerceArray<T = unknown>(value: unknown, defaultValue: T[] = []): T[] {
  if (value === null || value === undefined) {
    return defaultValue;
  }

  if (Array.isArray(value)) {
    // Filter out null/undefined values
    return value.filter(item => item !== null && item !== undefined) as T[];
  }

  // If it's an object that looks like an array (has numeric keys), try to convert
  if (typeof value === 'object') {
    const obj = value as Record<string, unknown>;
    const keys = Object.keys(obj);
    const isArrayLike = keys.every(key => !isNaN(Number(key)));
    if (isArrayLike) {
      return (
        keys
          .sort((a, b) => Number(a) - Number(b))
          // eslint-disable-next-line security/detect-object-injection -- Safe: numeric keys sorted and validated
          .map(key => obj[key])
          .filter(item => item !== null && item !== undefined) as T[]
      );
    }
  }

  return defaultValue;
}

/**
 * Coerce a value to an object
 */
export function coerceObject<T extends Record<string, unknown>>(
  value: unknown,
  defaultValue: T
): T {
  if (value === null || value === undefined) {
    return defaultValue;
  }

  // Check if it's a plain object
  if (typeof value === 'object' && !Array.isArray(value)) {
    return value as T;
  }

  // For non-objects, return default
  return defaultValue;
}

/**
 * Validate and coerce BirdNET settings
 */
export function coerceBirdNetSettings(settings: PartialBirdNetSettings): PartialBirdNetSettings {
  const coerced = { ...settings };

  // Sensitivity: 0.5 to 1.5
  if ('sensitivity' in settings) {
    coerced.sensitivity = coerceNumber(settings.sensitivity, 0.5, 1.5, 1.25);
  }

  // Threshold: 0 to 1
  if ('threshold' in settings) {
    coerced.threshold = coerceNumber(settings.threshold, 0, 1, 0.8);
  }

  // Overlap: 0 to 100 (percentage)
  if ('overlap' in settings) {
    coerced.overlap = coerceNumber(settings.overlap, 0, 100, 0);
  }

  // Latitude: -90 to 90
  if ('latitude' in settings) {
    coerced.latitude = coerceNumber(settings.latitude, -90, 90, 0);
  }

  // Longitude: -180 to 180
  if ('longitude' in settings) {
    coerced.longitude = coerceNumber(settings.longitude, -180, 180, 0);
  }

  // Threads: 0 to 32 (0 means use all available threads in backend)
  if ('threads' in settings) {
    coerced.threads = coerceNumber(settings.threads, 0, 32, 0);
  }

  // Dynamic threshold nested settings
  if (settings.dynamicThreshold && typeof settings.dynamicThreshold === 'object') {
    const dt = settings.dynamicThreshold as UnknownSettings;
    coerced.dynamicThreshold = {
      ...dt,
      enabled: coerceBoolean(dt.enabled, false),
      debug: coerceBoolean(dt.debug, false),
      trigger: coerceNumber(dt.trigger, 0, 100, 5),
      min: coerceNumber(dt.min, 0, 1, 0.1),
      validHours: coerceNumber(dt.validHours, 1, 168, 24),
    };
  }

  return coerced;
}

/**
 * Validate and coerce audio settings
 */
export function coerceAudioSettings(settings: PartialAudioSettings): PartialAudioSettings {
  const coerced = { ...settings };

  // Capture duration: 1 to 3600 seconds (1 hour max)
  if ('captureDuration' in settings) {
    coerced.captureDuration = coerceNumber(settings.captureDuration, 1, 3600, 3);
  }

  // Buffer size: reasonable limits
  if ('bufferSize' in settings) {
    coerced.bufferSize = coerceNumber(settings.bufferSize, 512, 67108864, 4096);
  }

  // Sample rate: specific valid values
  if ('sampleRate' in settings) {
    const validRates = [16000, 22050, 24000, 44100, 48000];
    const rate = coerceNumber(settings.sampleRate, 16000, 48000, 48000);
    // Find closest valid rate
    coerced.sampleRate = validRates.reduce((prev, curr) =>
      Math.abs(curr - rate) < Math.abs(prev - rate) ? curr : prev
    );
  }

  // Equalizer settings
  if ('equalizer' in settings && settings.equalizer && typeof settings.equalizer === 'object') {
    const eq = settings.equalizer as unknown as UnknownSettings;
    coerced.equalizer = {
      enabled: coerceBoolean(eq.enabled, false),
      filters: coerceArray(eq.filters, [])
        .map(filter => {
          // Type guard for valid filter objects
          // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- filter could be null from coerceArray
          if (!filter || typeof filter !== 'object' || Array.isArray(filter)) {
            return null; // Will be filtered out
          }

          const f = filter as UnknownSettings;

          // Normalize and validate filter type - backend expects proper case
          // BandStop and Notch are aliases for BandReject (DSP module treats as synonyms)
          const allowedTypesMap = {
            lowpass: 'LowPass',
            highpass: 'HighPass',
            bandpass: 'BandPass',
            bandstop: 'BandReject',
            bandreject: 'BandReject',
            notch: 'BandReject',
          };
          const rawType = coerceString(f.type, 'LowPass').toLowerCase();
          const normalizedType =
            allowedTypesMap[rawType as keyof typeof allowedTypesMap] || 'LowPass';

          // Determine default frequency based on filter type
          let defaultFrequency = 15000; // Default for LowPass
          if (normalizedType === 'HighPass') {
            defaultFrequency = 100;
          } else if (normalizedType === 'BandReject' || normalizedType === 'BandPass') {
            defaultFrequency = 1000;
          }

          const coercedFilter: EqualizerFilter = {
            id: coerceString(
              f.id,
              `filter_${Date.now()}_${Math.random().toString(36).slice(2, 11)}`
            ),
            type: normalizedType as EqualizerFilter['type'],
            frequency: coerceNumber(f.frequency, 20, 20000, defaultFrequency),
            q: coerceNumber(f.q, 0.1, 10, 0.707),
            width: coerceNumber(f.width, 1, 10000, 100), // Bandwidth in Hz
            gain: coerceNumber(f.gain, -48, 12, 0),
            passes: 1, // Default passes
          };

          // Set proper default passes based on filter type
          if (typeof f.passes === 'number') {
            coercedFilter.passes = coerceNumber(f.passes, 0, 4, 1);
          } else {
            // Default to 1 pass (12dB) for HighPass/LowPass/BandReject filters
            if (
              normalizedType === 'HighPass' ||
              normalizedType === 'LowPass' ||
              normalizedType === 'BandReject'
            ) {
              coercedFilter.passes = 1;
            } else {
              coercedFilter.passes = 0; // 0dB for other filter types initially
            }
          }

          return coercedFilter;
        })
        .filter(Boolean) as EqualizerFilter[], // Remove falsy entries and ensure proper typing
    };
  }

  // Export settings validation
  if ('export' in settings && settings.export && typeof settings.export === 'object') {
    const exp = settings.export as unknown as Record<string, unknown>;
    const coercedExport: Record<string, unknown> = { ...exp };

    // Always coerce enabled to boolean to ensure stable type
    coercedExport.enabled = coerceBoolean(exp.enabled, false);

    // Clamp capture length between 10 and 60 seconds (backend validation)
    if ('length' in exp) {
      coercedExport.length = coerceNumber(exp.length, 10, 60, 15);
    }

    // After clamping length, clamp pre-capture to max 50% of capture length
    const captureLength = coerceNumber(coercedExport.length, 10, 60, 15);
    if ('preCapture' in exp) {
      const maxPreCapture = Math.floor(captureLength / 2);
      coercedExport.preCapture = coerceNumber(exp.preCapture, 0, maxPreCapture, 3);
    }

    // Clamp gain between -40 and +40 dB (backend validation)
    if ('gain' in exp) {
      coercedExport.gain = coerceNumber(exp.gain, -40, 40, 0);
    }

    // Normalization settings
    if ('normalization' in exp && exp.normalization && typeof exp.normalization === 'object') {
      const norm = exp.normalization as Record<string, unknown>;
      const normalizationSettings: Record<string, unknown> = {
        ...norm,
        enabled: coerceBoolean(norm.enabled, false),
      };

      // Only clamp values if they exist, preserving enabled state
      if ('targetLUFS' in norm) {
        normalizationSettings.targetLUFS = coerceNumber(norm.targetLUFS, -40, -10, -23);
      }
      if ('loudnessRange' in norm) {
        normalizationSettings.loudnessRange = coerceNumber(norm.loudnessRange, 0, 20, 7);
      }
      if ('truePeak' in norm) {
        normalizationSettings.truePeak = coerceNumber(norm.truePeak, -10, 0, -2);
      }

      coercedExport.normalization = normalizationSettings;
    }

    // Cast back to appropriate type for assignment
    coerced.export = coercedExport as unknown as typeof settings.export;
  }

  return coerced;
}

/**
 * Validate and coerce security settings
 */
export function coerceSecuritySettings(settings: PartialSecuritySettings): PartialSecuritySettings {
  const coerced = { ...settings };

  // Basic auth
  if (settings.basicAuth && typeof settings.basicAuth === 'object') {
    const ba = settings.basicAuth as UnknownSettings;
    coerced.basicAuth = {
      ...ba,
      enabled: coerceBoolean(ba.enabled, false),
      username: coerceString(ba.username, ''),
      password: coerceString(ba.password, ''),
    };
  }

  // OAuth settings - handle googleAuth and githubAuth explicitly
  if (settings.googleAuth && typeof settings.googleAuth === 'object') {
    const auth = settings.googleAuth as Partial<OAuthSettings> & UnknownSettings;
    coerced.googleAuth = {
      ...auth,
      enabled: coerceBoolean(auth.enabled, false),
      clientId: coerceString(auth.clientId, ''),
      clientSecret: coerceString(auth.clientSecret, ''),
      redirectURI: coerceString(auth.redirectURI, ''),
      userId: coerceString(auth.userId, ''),
    };
  }

  if (settings.githubAuth && typeof settings.githubAuth === 'object') {
    const auth = settings.githubAuth as Partial<OAuthSettings> & UnknownSettings;
    coerced.githubAuth = {
      ...auth,
      enabled: coerceBoolean(auth.enabled, false),
      clientId: coerceString(auth.clientId, ''),
      clientSecret: coerceString(auth.clientSecret, ''),
      redirectURI: coerceString(auth.redirectURI, ''),
      userId: coerceString(auth.userId, ''),
    };
  }

  // Auto TLS - handle as boolean
  if ('autoTls' in settings) {
    coerced.autoTls = coerceBoolean(settings.autoTls, false);
  } else if ('autoTLS' in settings) {
    // Handle legacy uppercase property name
    coerced.autoTls = coerceBoolean(settings.autoTLS, false);
  }

  return coerced;
}

/**
 * Validate and coerce species settings
 */
export function coerceSpeciesSettings(
  settings: PartialSpeciesSettings | null | undefined
): PartialSpeciesSettings {
  // Handle case where settings is null, undefined, or not an object
  const safeSettings = settings && typeof settings === 'object' ? settings : {};

  const coerced: PartialSpeciesSettings = {
    include: coerceArray<string>(safeSettings.include, []),
    exclude: coerceArray<string>(safeSettings.exclude, []),
    config: coerceObject<Record<string, SpeciesConfig>>(
      safeSettings.config as UnknownSettings,
      {} as Record<string, SpeciesConfig>
    ),
  };

  // Validate and clean species config
  const cleanConfig: Record<string, SpeciesConfig> = {};

  for (const [species, config] of Object.entries(coerced.config ?? {})) {
    // Values from Object.entries can be any type, filter for objects
    // eslint-disable-next-line @typescript-eslint/no-unnecessary-condition -- config could be null from user input
    if (typeof config === 'object' && config !== null && !Array.isArray(config)) {
      const configObj = config as unknown as UnknownSettings;
      const speciesConfig: SpeciesConfig = {
        threshold: coerceNumber(configObj.threshold, 0, 1, 0.8),
        interval: coerceNumber(configObj.interval, 0, 3600, 0),
        actions: coerceArray<Action>(configObj.actions, []),
      };

      // Filter out actions with empty commands
      if (speciesConfig.actions.length > 0) {
        speciesConfig.actions = speciesConfig.actions.filter((action: Action) => {
          if (typeof action !== 'object') return false;
          if (!action.command || typeof action.command !== 'string') return false;
          return action.command.trim().length > 0;
        });
      }

      // eslint-disable-next-line security/detect-object-injection -- Safe: species key from Object.entries
      cleanConfig[species] = speciesConfig;
    }
  }

  coerced.config = cleanConfig;

  return coerced;
}

/**
 * Validate and coerce MQTT settings
 */
export function coerceMQTTSettings(settings: PartialMQTTSettings): PartialMQTTSettings {
  const coerced = {
    ...settings,
    enabled: coerceBoolean(settings.enabled, false),
    broker: coerceString(settings.broker, ''),
    port: coerceNumber(settings.port, 1, 65535, 1883),
    username: coerceString(settings.username, ''),
    password: coerceString(settings.password, ''),
    topic: coerceString(settings.topic, ''),
    retain: coerceBoolean(settings.retain, false),
  };

  // TLS settings
  if (settings.tls && typeof settings.tls === 'object') {
    const tls = settings.tls as UnknownSettings;
    coerced.tls = {
      enabled: coerceBoolean(tls.enabled, false),
      skipVerify: coerceBoolean(tls.skipVerify, false),
    };
  } else {
    // Provide default TLS settings if missing
    coerced.tls = {
      enabled: false,
      skipVerify: false,
    };
  }

  return coerced;
}

/**
 * Validate and coerce false positive filter settings
 */
export function coerceFalsePositiveFilterSettings(
  settings: PartialFalsePositiveFilterSettings | null | undefined
): PartialFalsePositiveFilterSettings {
  const safeSettings = settings && typeof settings === 'object' ? settings : {};

  return {
    level: coerceNumber(safeSettings.level, 0, 5, 0),
  };
}

/**
 * Validate and coerce webhook endpoint configuration
 */
function coerceWebhookEndpoint(endpoint: unknown): WebhookEndpointConfig | null {
  if (!endpoint || typeof endpoint !== 'object' || Array.isArray(endpoint)) {
    return null;
  }

  const e = endpoint as UnknownSettings;
  const url = coerceString(e.url, '');

  // Skip endpoints with no URL
  if (!url) {
    return null;
  }

  // Validate URL scheme - only allow http(s) for webhooks
  try {
    const parsed = new URL(url);
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
      return null;
    }
  } catch {
    // Invalid URL format
    return null;
  }

  // Normalize HTTP method to uppercase
  const rawMethod = coerceString(e.method, 'POST').toUpperCase();
  const validMethods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'];
  const method = validMethods.includes(rawMethod) ? rawMethod : 'POST';

  const coercedEndpoint: WebhookEndpointConfig = {
    ...(e as WebhookEndpointConfig), // Preserve backend-only fields
    url,
    method,
  };

  // Coerce headers if present - validate each value is a string
  if (e.headers && typeof e.headers === 'object' && !Array.isArray(e.headers)) {
    const rawHeaders = e.headers as Record<string, unknown>;
    const validatedHeaders: Record<string, string> = {};

    for (const [key, value] of Object.entries(rawHeaders)) {
      if (typeof value === 'string') {
        // eslint-disable-next-line security/detect-object-injection -- key from Object.entries
        validatedHeaders[key] = value;
      } else if (value !== null && value !== undefined) {
        // Convert primitives to strings, skip objects/arrays
        if (typeof value === 'number' || typeof value === 'boolean') {
          // eslint-disable-next-line security/detect-object-injection -- key from Object.entries
          validatedHeaders[key] = String(value);
        }
        // Skip non-primitive values (objects, arrays, functions)
      }
    }

    // Only set headers if we have valid entries
    if (Object.keys(validatedHeaders).length > 0) {
      coercedEndpoint.headers = validatedHeaders;
    }
  }

  // Coerce auth if present
  if (e.auth && typeof e.auth === 'object' && !Array.isArray(e.auth)) {
    const auth = e.auth as UnknownSettings;
    // Normalize auth type to lowercase for case-insensitive matching
    const authType = coerceString(auth.type, 'none').toLowerCase();
    const validAuthTypes = ['none', 'bearer', 'basic', 'custom'];
    const normalizedType = validAuthTypes.includes(authType) ? authType : 'none';

    coercedEndpoint.auth = {
      type: normalizedType as WebhookAuthConfig['type'],
      token: coerceString(auth.token, ''),
      user: coerceString(auth.user, ''),
      pass: coerceString(auth.pass, ''),
      header: coerceString(auth.header, ''),
      value: coerceString(auth.value, ''),
    };
  }

  return coercedEndpoint;
}

/**
 * Validate and coerce push provider configuration
 */
function coercePushProvider(provider: unknown): PushProviderConfig | null {
  if (!provider || typeof provider !== 'object' || Array.isArray(provider)) {
    return null;
  }

  const p = provider as UnknownSettings;
  const providerType = coerceString(p.type, 'shoutrrr');
  const validTypes = ['shoutrrr', 'webhook', 'script'];
  const normalizedType = validTypes.includes(providerType) ? providerType : 'shoutrrr';

  const coercedProvider: PushProviderConfig = {
    ...(p as PushProviderConfig), // Preserve backend-only fields (command, args, environment, template, etc.)
    type: normalizedType as PushProviderConfig['type'],
    enabled: coerceBoolean(p.enabled, false),
    name: coerceString(p.name, ''),
  };

  // Coerce URLs only for shoutrrr providers
  if (normalizedType === 'shoutrrr' && p.urls !== undefined) {
    coercedProvider.urls = coerceArray<string>(p.urls, []).filter(
      url => typeof url === 'string' && url.trim() !== ''
    );
  }

  // Coerce endpoints only for webhook providers
  if (normalizedType === 'webhook' && p.endpoints !== undefined) {
    const endpoints = coerceArray(p.endpoints, []);
    coercedProvider.endpoints = endpoints
      .map(coerceWebhookEndpoint)
      .filter((e): e is WebhookEndpointConfig => e !== null);
  }

  // Coerce filter if present
  if (p.filter && typeof p.filter === 'object' && !Array.isArray(p.filter)) {
    const f = p.filter as UnknownSettings;
    const coercedFilter: PushFilterConfig = {};

    // Helper to coerce filter arrays - enforce string types and trim
    const coerceStringArray = (value: unknown): string[] =>
      coerceArray<unknown>(value, [])
        .filter((v): v is string => typeof v === 'string')
        .map(s => s.trim())
        .filter(s => s !== '');

    if (f.types !== undefined) {
      coercedFilter.types = coerceStringArray(f.types);
    }
    if (f.priorities !== undefined) {
      coercedFilter.priorities = coerceStringArray(f.priorities);
    }
    if (f.components !== undefined) {
      coercedFilter.components = coerceStringArray(f.components);
    }

    coercedProvider.filter = coercedFilter;
  }

  return coercedProvider;
}

/**
 * Validate and coerce push settings
 */
function coercePushSettings(settings: unknown): PushSettings {
  if (!settings || typeof settings !== 'object' || Array.isArray(settings)) {
    return {
      enabled: false,
      providers: [],
      minConfidenceThreshold: 0,
      speciesCooldownMinutes: 0,
    };
  }

  const s = settings as UnknownSettings;

  return {
    ...(s as PushSettings), // Preserve backend-only fields (default_timeout, circuit_breaker, health_check, etc.)
    enabled: coerceBoolean(s.enabled, false),
    providers: coerceArray(s.providers, [])
      .map(coercePushProvider)
      .filter((p): p is PushProviderConfig => p !== null),
    // Detection filtering settings (0 = disabled)
    minConfidenceThreshold: coerceNumber(s.minConfidenceThreshold, 0, 1, 0),
    speciesCooldownMinutes: coerceNumber(s.speciesCooldownMinutes, 0, 1440, 0), // Max 24 hours
  };
}

/**
 * Validate and coerce notification settings
 */
export function coerceNotificationSettings(
  settings: PartialNotificationSettings | null | undefined
): PartialNotificationSettings {
  const safeSettings = settings && typeof settings === 'object' ? settings : {};

  const coerced: PartialNotificationSettings = {};

  // Coerce push settings
  if ('push' in safeSettings) {
    coerced.push = coercePushSettings(safeSettings.push);
  }

  // Coerce templates if present - ensure it's a plain object, not an array
  if (
    safeSettings.templates &&
    typeof safeSettings.templates === 'object' &&
    !Array.isArray(safeSettings.templates)
  ) {
    const templates = safeSettings.templates as UnknownSettings;
    coerced.templates = {};

    // Ensure newSpecies is also a plain object, not an array
    if (
      templates.newSpecies &&
      typeof templates.newSpecies === 'object' &&
      !Array.isArray(templates.newSpecies)
    ) {
      const ns = templates.newSpecies as UnknownSettings;
      coerced.templates.newSpecies = {
        title: coerceString(ns.title, ''),
        message: coerceString(ns.message, ''),
      };
    }
  }

  return coerced;
}

/**
 * Main coercion function for all settings
 */
export function coerceSettings(section: string, data: UnknownSettings): UnknownSettings {
  switch (section) {
    case 'birdnet':
      return coerceBirdNetSettings(data as PartialBirdNetSettings);
    case 'audio':
      return coerceAudioSettings(data as PartialAudioSettings);
    case 'realtime': {
      // Handle realtime nested structures
      const coercedRealtime: UnknownSettings = { ...data };

      if (Object.prototype.hasOwnProperty.call(data, 'audio')) {
        coercedRealtime.audio = coerceAudioSettings(data.audio as PartialAudioSettings);
      }

      if (Object.prototype.hasOwnProperty.call(data, 'mqtt')) {
        coercedRealtime.mqtt = coerceMQTTSettings(data.mqtt as PartialMQTTSettings);
      }

      if (Object.prototype.hasOwnProperty.call(data, 'species')) {
        coercedRealtime.species = coerceSpeciesSettings(data.species as PartialSpeciesSettings);
      }

      if (Object.prototype.hasOwnProperty.call(data, 'falsePositiveFilter')) {
        coercedRealtime.falsePositiveFilter = coerceFalsePositiveFilterSettings(
          data.falsePositiveFilter as PartialFalsePositiveFilterSettings
        );
      }

      return coercedRealtime;
    }
    case 'security':
      return coerceSecuritySettings(data as PartialSecuritySettings);
    case 'species':
      return coerceSpeciesSettings(data as PartialSpeciesSettings);
    case 'mqtt':
      return coerceMQTTSettings(data as PartialMQTTSettings);
    case 'notification':
      return coerceNotificationSettings(data as PartialNotificationSettings);
    default:
      return data;
  }
}
