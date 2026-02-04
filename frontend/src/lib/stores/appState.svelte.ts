/**
 * appState.svelte.ts
 *
 * Centralized application state management using Svelte 5 runes.
 * This module provides a single source of truth for:
 * - CSRF token for secure API requests
 * - Security/authentication configuration
 * - Application version
 * - Initialization state with retry logic
 *
 * Usage:
 *   import { appState, initApp, getCsrfToken } from '$lib/stores/appState.svelte';
 *
 *   // In App.svelte (once at startup):
 *   const success = await initApp();
 *   if (!success) { // show error page }
 *
 *   // Anywhere else - reactive access:
 *   {appState.version}
 *   {appState.security.enabled}
 *
 *   // For API utilities:
 *   const token = getCsrfToken();
 */
/* eslint-disable no-undef */
import type { AuthConfig } from '../../app.d';
import { getLogger } from '../utils/logger';
import { buildAppUrl, setBasePath } from '../utils/urlHelpers';

const logger = getLogger('appState');

/** API endpoint for app configuration */
const CONFIG_ENDPOINT = '/api/v2/app/config';

/** Maximum number of retry attempts for config fetch */
export const MAX_RETRIES = 3;

/** Retry delays in milliseconds (exponential backoff) */
const RETRY_DELAYS = [1000, 2000, 4000];

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
      enabledProviders: string[];
    };
  };
  version: string;
  basePath?: string;
}

/**
 * Application state interface
 */
interface AppState {
  /** Whether initialization has completed (success or failure) */
  initialized: boolean;
  /** Whether initialization is currently in progress */
  loading: boolean;
  /** Error message if initialization failed after all retries */
  error: string | null;
  /** Current retry attempt (0-based) */
  retryCount: number;
  /** CSRF token for secure API requests */
  csrfToken: string;
  /** Application version string */
  version: string;
  /** Security configuration */
  security: {
    enabled: boolean;
    accessAllowed: boolean;
    authConfig: AuthConfig;
  };
}

/**
 * Default state values
 */
const DEFAULT_STATE: AppState = {
  initialized: false,
  loading: false,
  error: null,
  retryCount: 0,
  csrfToken: '',
  version: 'Development Build',
  security: {
    enabled: false,
    accessAllowed: true,
    authConfig: {
      basicEnabled: false,
      enabledProviders: [],
    },
  },
};

/**
 * Centralized application state using Svelte 5 $state rune.
 * This is a reactive object that can be imported and used directly in components.
 */
export const appState: AppState = $state({ ...DEFAULT_STATE });

/**
 * Delays execution for the specified number of milliseconds.
 */
function delay(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/**
 * Fetches the application configuration from the backend API.
 * Uses AbortController for timeout handling.
 */
async function fetchConfig(): Promise<AppConfigResponse> {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);

  try {
    const response = await fetch(buildAppUrl(CONFIG_ENDPOINT), {
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
    return data;
  } finally {
    clearTimeout(timeoutId);
  }
}

/**
 * Initializes the application by fetching configuration from the backend API.
 * Implements retry logic with exponential backoff.
 *
 * @returns true if initialization succeeded, false if all retries failed
 */
export async function initApp(): Promise<boolean> {
  // Prevent multiple concurrent initializations
  if (appState.loading) {
    logger.warn('initApp called while already loading');
    return false;
  }

  // Reset state for fresh initialization
  appState.loading = true;
  appState.error = null;
  appState.retryCount = 0;

  for (let attempt = 0; attempt <= MAX_RETRIES; attempt++) {
    appState.retryCount = attempt;

    try {
      logger.info(`Fetching app configuration (attempt ${attempt + 1}/${MAX_RETRIES + 1})`);

      const config = await fetchConfig();

      // Set the authoritative base path from the backend before updating other state.
      // This ensures all subsequent buildAppUrl() calls use the correct value.
      setBasePath(config.basePath ?? '');

      // Update state with fetched configuration
      appState.csrfToken = config.csrfToken;
      appState.version = config.version;
      appState.security = {
        enabled: config.security.enabled,
        accessAllowed: config.security.accessAllowed,
        authConfig: {
          basicEnabled: config.security.authConfig.basicEnabled,
          enabledProviders: config.security.authConfig.enabledProviders,
        },
      };

      appState.initialized = true;
      appState.loading = false;
      appState.error = null;

      logger.info('App configuration loaded successfully', {
        securityEnabled: appState.security.enabled,
        version: appState.version,
      });

      return true;
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Unknown error';

      if (error instanceof Error && error.name === 'AbortError') {
        logger.error(`Config fetch timed out (attempt ${attempt + 1})`, {
          timeout: FETCH_TIMEOUT_MS,
        });
      } else {
        logger.error(`Config fetch failed (attempt ${attempt + 1})`, error);
      }

      // If we have more retries, wait before trying again
      if (attempt < MAX_RETRIES) {
        // eslint-disable-next-line security/detect-object-injection
        const delayMs = RETRY_DELAYS[attempt];
        logger.info(`Retrying in ${delayMs}ms...`);
        await delay(delayMs);
      } else {
        // All retries exhausted
        appState.error = `Failed to load application configuration after ${MAX_RETRIES + 1} attempts: ${errorMessage}`;
        appState.initialized = true; // Mark as initialized (with error)
        appState.loading = false;

        logger.error('All config fetch retries exhausted', {
          attempts: MAX_RETRIES + 1,
          lastError: errorMessage,
        });

        return false;
      }
    }
  }

  // This should not be reached, but TypeScript needs it
  return false;
}

/**
 * Gets the CSRF token for API requests.
 * This is the primary method for api.ts to retrieve the token.
 *
 * @returns The CSRF token or empty string if not available
 */
export function getCsrfToken(): string {
  return appState.csrfToken;
}

/**
 * Checks if security is enabled.
 *
 * @returns True if security is enabled
 */
export function isSecurityEnabled(): boolean {
  return appState.security.enabled;
}

/**
 * Checks if the current user has access.
 *
 * @returns True if access is allowed
 */
export function isAccessAllowed(): boolean {
  return appState.security.accessAllowed;
}

/**
 * Gets the authentication configuration.
 *
 * @returns The auth config
 */
export function getAuthConfig(): AuthConfig {
  return appState.security.authConfig;
}

/**
 * Gets the application version string.
 *
 * @returns The version string
 */
export function getVersion(): string {
  return appState.version;
}

/**
 * Updates the access allowed state.
 * Used after successful login to update the UI.
 *
 * @param allowed Whether access is now allowed
 */
export function setAccessAllowed(allowed: boolean): void {
  appState.security.accessAllowed = allowed;
}

/**
 * Updates the CSRF token.
 * Used when receiving a new token from the server.
 *
 * @param token The new CSRF token
 */
export function setCsrfToken(token: string): void {
  appState.csrfToken = token;
}
