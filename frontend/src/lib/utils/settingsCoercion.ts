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
} from '$lib/stores/settings';

// Type for partial/unknown settings data
type UnknownSettings = Record<string, unknown>;
type PartialBirdNetSettings = Partial<BirdNetSettings> & UnknownSettings;
type PartialAudioSettings = Partial<AudioSettings> & UnknownSettings;
type PartialSecuritySettings = Partial<SecuritySettings> & UnknownSettings;
type PartialSpeciesSettings = Partial<SpeciesSettings> & UnknownSettings;
type PartialMQTTSettings = Partial<MQTTSettings> & UnknownSettings;

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
          const allowedTypesMap = {
            lowpass: 'LowPass',
            highpass: 'HighPass',
            bandpass: 'BandPass',
            bandstop: 'BandStop',
          };
          const rawType = coerceString(f.type, 'LowPass').toLowerCase();
          const normalizedType =
            allowedTypesMap[rawType as keyof typeof allowedTypesMap] || 'LowPass';

          const coercedFilter: EqualizerFilter = {
            id: coerceString(
              f.id,
              `filter_${Date.now()}_${Math.random().toString(36).slice(2, 11)}`
            ),
            type: normalizedType as EqualizerFilter['type'],
            frequency: coerceNumber(
              f.frequency,
              20,
              20000,
              normalizedType === 'HighPass' ? 100 : 15000
            ),
            q: coerceNumber(f.q, 0.1, 10, 0.707),
            gain: coerceNumber(f.gain, -48, 12, 0),
            passes: 1, // Default passes
          };

          // Set proper default passes based on filter type
          if (typeof f.passes === 'number') {
            coercedFilter.passes = coerceNumber(f.passes, 0, 4, 1);
          } else {
            // Default to 1 pass (12dB) for HighPass/LowPass filters
            if (normalizedType === 'HighPass' || normalizedType === 'LowPass') {
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

      return coercedRealtime;
    }
    case 'security':
      return coerceSecuritySettings(data as PartialSecuritySettings);
    case 'species':
      return coerceSpeciesSettings(data as PartialSpeciesSettings);
    case 'mqtt':
      return coerceMQTTSettings(data as PartialMQTTSettings);
    default:
      return data;
  }
}
