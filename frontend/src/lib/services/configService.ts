/**
 * configService.ts
 *
 * Application configuration service that fetches runtime configuration from the backend API.
 * This replaces the previous window.BIRDNET_CONFIG approach which required server-side
 * template rendering.
 *
 * The service:
 * - Fetches configuration from /api/v2/app/config on app initialization
 * - Caches the configuration for the session lifetime
 * - Provides typed access to security settings, auth providers, and version info
 * - Manages the CSRF token for secure API requests
 *
 * Usage:
 *   import { initAppConfig, getAppConfig, getCsrfToken } from '$lib/services/configService';
 *
 *   // In App.svelte or main entry point:
 *   await initAppConfig();
 *
 *   // Anywhere else:
 *   const config = getAppConfig();
 *   const csrfToken = getCsrfToken();
 */

import type { AuthConfig, BirdnetConfig } from '../../app.d';
import { getLogger } from '../utils/logger';

const logger = getLogger('configService');

/** API endpoint for app configuration */
const CONFIG_ENDPOINT = '/api/v2/app/config';

/** Default timeout for config fetch in milliseconds */
const FETCH_TIMEOUT_MS = 10000;

/**
 * API response type from /api/v2/app/config endpoint
 */
interface AppConfigResponse {
  csrfToken: string;
  security: {
    enabled: boolean;
    accessAllowed: boolean;
    authConfig: {
      basicEnabled: boolean;
      googleEnabled: boolean;
      githubEnabled: boolean;
      microsoftEnabled: boolean;
    };
  };
  version: string;
}

/**
 * Internal state for the config service
 */
let cachedConfig: BirdnetConfig | null = null;
let configPromise: Promise<BirdnetConfig> | null = null;
let isInitialized = false;

/**
 * Default configuration used when API fetch fails
 */
const DEFAULT_CONFIG: BirdnetConfig = {
  csrfToken: '',
  security: {
    enabled: false,
    accessAllowed: true,
    authConfig: {
      basicEnabled: false,
      googleEnabled: false,
      githubEnabled: false,
      microsoftEnabled: false,
    },
  },
  version: 'Development Build',
};

/**
 * Transforms the API response into the BirdnetConfig format
 */
function transformApiResponse(response: AppConfigResponse): BirdnetConfig {
  return {
    csrfToken: response.csrfToken,
    security: {
      enabled: response.security.enabled,
      accessAllowed: response.security.accessAllowed,
      authConfig: {
        basicEnabled: response.security.authConfig.basicEnabled,
        googleEnabled: response.security.authConfig.googleEnabled,
        githubEnabled: response.security.authConfig.githubEnabled,
        microsoftEnabled: response.security.authConfig.microsoftEnabled,
      },
    },
    version: response.version,
    currentPath: typeof window !== 'undefined' ? window.location.pathname : '/',
  };
}

/**
 * Fetches the application configuration from the backend API.
 * Uses a timeout to prevent hanging on slow connections.
 */
async function fetchConfig(): Promise<BirdnetConfig> {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);

  try {
    const response = await fetch(CONFIG_ENDPOINT, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
      },
      credentials: 'same-origin',
      signal: controller.signal,
    });

    if (!response.ok) {
      throw new Error(`Config fetch failed: ${response.status} ${response.statusText}`);
    }

    const data: AppConfigResponse = await response.json();
    const config = transformApiResponse(data);

    logger.info('App configuration loaded successfully', {
      securityEnabled: config.security?.enabled,
      version: config.version,
    });

    return config;
  } catch (error) {
    if (error instanceof Error && error.name === 'AbortError') {
      logger.error('Config fetch timed out', { timeout: FETCH_TIMEOUT_MS });
    } else {
      logger.error('Failed to fetch app configuration', error);
    }
    throw error;
  } finally {
    clearTimeout(timeoutId);
  }
}

/**
 * Initializes the application configuration by fetching from the backend API.
 * This should be called once at application startup, before rendering the main app.
 *
 * If the fetch fails, a default configuration is used to allow the app to render
 * in a degraded state (e.g., showing a login prompt or error message).
 *
 * @returns The loaded configuration
 */
export async function initAppConfig(): Promise<BirdnetConfig> {
  // Return cached config if already initialized
  if (isInitialized && cachedConfig) {
    return cachedConfig;
  }

  // Return existing promise if initialization is in progress
  if (configPromise) {
    return configPromise;
  }

  // Start fetching
  configPromise = fetchConfig()
    .then(config => {
      cachedConfig = config;
      isInitialized = true;

      // Also set it on window for backwards compatibility during migration
      if (typeof window !== 'undefined') {
        window.BIRDNET_CONFIG = config;
      }

      return config;
    })
    .catch(() => {
      // Use default config on failure
      logger.warn('Using default configuration due to fetch failure');
      cachedConfig = DEFAULT_CONFIG;
      isInitialized = true;

      if (typeof window !== 'undefined') {
        window.BIRDNET_CONFIG = DEFAULT_CONFIG;
      }

      return DEFAULT_CONFIG;
    })
    .finally(() => {
      configPromise = null;
    });

  return configPromise;
}

/**
 * Gets the current application configuration.
 * Returns null if the configuration hasn't been loaded yet.
 *
 * @returns The cached configuration or null
 */
export function getAppConfig(): BirdnetConfig | null {
  return cachedConfig;
}

/**
 * Gets the CSRF token from the loaded configuration.
 * Returns an empty string if configuration hasn't been loaded.
 *
 * @returns The CSRF token or empty string
 */
export function getCsrfToken(): string {
  return cachedConfig?.csrfToken ?? '';
}

/**
 * Checks if security is enabled based on the loaded configuration.
 *
 * @returns True if any auth method is enabled
 */
export function isSecurityEnabled(): boolean {
  return cachedConfig?.security?.enabled ?? false;
}

/**
 * Checks if the current user has access (authenticated or security disabled).
 *
 * @returns True if access is allowed
 */
export function isAccessAllowed(): boolean {
  return cachedConfig?.security?.accessAllowed ?? true;
}

/**
 * Gets the authentication configuration.
 *
 * @returns The auth config or default values
 */
export function getAuthConfig(): AuthConfig {
  return (
    cachedConfig?.security?.authConfig ?? {
      basicEnabled: false,
      googleEnabled: false,
      githubEnabled: false,
      microsoftEnabled: false,
    }
  );
}

/**
 * Gets the application version string.
 *
 * @returns The version string
 */
export function getVersion(): string {
  return cachedConfig?.version ?? 'Development Build';
}

/**
 * Forces a refresh of the configuration from the API.
 * This can be used after login/logout to update the access status.
 *
 * @returns The refreshed configuration
 */
export async function refreshConfig(): Promise<BirdnetConfig> {
  isInitialized = false;
  cachedConfig = null;
  return initAppConfig();
}

/**
 * Checks if the configuration has been initialized.
 *
 * @returns True if config has been loaded
 */
export function isConfigInitialized(): boolean {
  return isInitialized;
}
