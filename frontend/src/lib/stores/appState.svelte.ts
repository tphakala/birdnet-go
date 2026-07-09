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
import type { AuthConfig } from '../../app.d';
import { getLogger, setSentryCaptureError } from '../utils/logger';
import { buildAppUrl, setBasePath } from '../utils/urlHelpers';
import { scheme } from './scheme';
import { logoStyle } from './logoStyle';

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
    publicAccess?: {
      liveAudio: boolean;
    };
    privateMode?: boolean;
  };
  version: string;
  /** Dataset version for the per-locale species-name dictionary. Used as a cache-buster. */
  speciesDictVersion?: string;
  freshInstall?: boolean;
  newVersion?: boolean;
  previousVersion?: string;
  basePath?: string;
  colorScheme?: string;
  customColors?: { primary: string; accent: string };
  logoStyle?: string;
  liveSpectrogram?: boolean;
  /** Whether audio clip export is enabled; drives showing per-detection spectrogram/audio in the UI */
  audioExportEnabled?: boolean;
  isEnhancedDatabase?: boolean;
  layout?: {
    elements: {
      id?: string;
      type: string;
      enabled: boolean;
      width?: string;
      banner?: Record<string, unknown>;
      video?: Record<string, unknown>;
      summary?: Record<string, unknown>;
      grid?: Record<string, unknown>;
    }[];
  };
  sentry?: {
    enabled: boolean;
    dsn: string;
    systemId: string;
  };
  projectLinks?: ProjectLinks;
}

/**
 * Project identity and routing links served by the backend (always present in
 * a normal config response). These drive in-app links ("View on GitHub",
 * "Report an Issue", the support-page issue links) so a fork that rebrands the
 * backend (via BIRDNET_GO_PROJECT_* env vars or ldflags) is reflected in the UI
 * without editing translations or component source.
 */
interface ProjectLinks {
  name: string;
  repoUrl: string;
  issuesUrl: string;
  newIssueUrl: string;
  supportUrl: string;
  discussionsUrl: string;
  releasesUrl: string;
  communityUrl: string;
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
  /** Whether this is a fresh install (no prior data) */
  freshInstall: boolean;
  /** Whether the app was updated to a new version */
  newVersion: boolean;
  /** Previous version before the update */
  previousVersion: string | null;
  /** Whether live spectrogram is enabled */
  liveSpectrogram: boolean;
  /** Whether audio clip export is enabled; when false, per-detection spectrogram/audio UI is hidden */
  audioExportEnabled: boolean;
  isEnhancedDatabase: boolean;
  /** Dataset version for the per-locale species-name dictionary. Empty string when unknown. */
  speciesDictVersion: string;
  /** Dashboard layout from public config (available before auth) */
  layout: AppConfigResponse['layout'] | null;
  /** Project identity/links for routing in-app links */
  projectLinks: ProjectLinks;
  /** Security configuration */
  security: {
    enabled: boolean;
    accessAllowed: boolean;
    authConfig: AuthConfig;
    publicAccess: {
      liveAudio: boolean;
    };
    privateMode: boolean;
  };
}

/**
 * Upstream project links used as a fallback before the backend config loads
 * (and on the pre-config server-error screen). Once the app-config response
 * arrives, these are replaced by the backend-resolved branding values, so a
 * fork's configured links take over for the entire authenticated UI.
 */
const DEFAULT_PROJECT_LINKS: ProjectLinks = {
  name: 'BirdNET-Go',
  repoUrl: 'https://github.com/tphakala/birdnet-go',
  issuesUrl: 'https://github.com/tphakala/birdnet-go/issues',
  newIssueUrl: 'https://github.com/tphakala/birdnet-go/issues/new',
  supportUrl: 'https://github.com/tphakala/birdnet-go',
  discussionsUrl: 'https://github.com/tphakala/birdnet-go/discussions',
  releasesUrl: 'https://github.com/tphakala/birdnet-go/releases',
  communityUrl: 'https://discord.gg/gcSCFGUtsd',
};

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
  freshInstall: false,
  newVersion: false,
  previousVersion: null,
  liveSpectrogram: false,
  audioExportEnabled: true,
  isEnhancedDatabase: false,
  speciesDictVersion: '',
  layout: null,
  projectLinks: DEFAULT_PROJECT_LINKS,
  security: {
    enabled: false,
    accessAllowed: true,
    authConfig: {
      basicEnabled: false,
      enabledProviders: [],
    },
    publicAccess: {
      liveAudio: false,
    },
    privateMode: false,
  },
};

/**
 * Centralized application state using Svelte 5 $state rune.
 * This is a reactive object that can be imported and used directly in components.
 */
export const appState: AppState = $state({ ...DEFAULT_STATE });

/** Whether frontend telemetry is enabled (set during initApp). */
let sentryEnabled = false;

/** Synchronous check for whether frontend telemetry is enabled. */
export function isSentryEnabled(): boolean {
  return sentryEnabled;
}

/**
 * Whether the current user has access to live audio features.
 * Centralized check: security disabled, user authenticated, or public live audio enabled.
 * Exported as a function because Svelte 5 does not allow exporting $derived from modules.
 */
export function hasLiveAudioAccess(): boolean {
  return (
    !appState.security.enabled ||
    appState.security.accessAllowed ||
    appState.security.publicAccess.liveAudio
  );
}

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
        publicAccess: {
          liveAudio: config.security.publicAccess?.liveAudio ?? false,
        },
        privateMode: config.security.privateMode ?? false,
      };

      appState.freshInstall = config.freshInstall ?? false;
      appState.newVersion = config.newVersion ?? false;
      appState.previousVersion = config.previousVersion ?? null;
      appState.liveSpectrogram = config.liveSpectrogram ?? false;
      appState.audioExportEnabled = config.audioExportEnabled ?? true;
      appState.isEnhancedDatabase = config.isEnhancedDatabase ?? false;
      appState.speciesDictVersion = config.speciesDictVersion ?? '';
      appState.layout = config.layout ?? null;
      appState.projectLinks = config.projectLinks ?? DEFAULT_PROJECT_LINKS;

      // Apply server-configured appearance settings
      if (config.colorScheme) {
        scheme.applyServerScheme(config.colorScheme, config.customColors);
      }
      if (config.logoStyle === 'gradient' || config.logoStyle === 'solid') {
        logoStyle.setStyle(config.logoStyle);
      }

      // Initialize frontend Sentry when telemetry is enabled
      const sentryConfig = config.sentry;
      sentryEnabled = false;
      setSentryCaptureError(null); // Clear any stale hook from previous init
      if (sentryConfig?.enabled && sentryConfig.dsn) {
        sentryEnabled = true;
        import('$lib/telemetry/sentry')
          .then(({ initSentry, captureError }) => {
            initSentry({
              dsn: sentryConfig.dsn,
              systemId: sentryConfig.systemId,
              version: config.version,
            });
            setSentryCaptureError(captureError);
            logger.info('Frontend Sentry initialized');
          })
          .catch(err => {
            setSentryCaptureError(null);
            logger.warn('Failed to initialize Sentry', err);
          });
      }

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
 * Refreshes the CSRF token by re-fetching /api/v2/app/config.
 * Called when a 403 suggests the CSRF cookie has expired.
 * Deduplicates concurrent calls so only one network request is made.
 *
 * @returns true if refresh succeeded, false otherwise
 */
let csrfRefreshPromise: Promise<boolean> | null = null;

export function refreshCsrfToken(): Promise<boolean> {
  if (csrfRefreshPromise) return csrfRefreshPromise;

  csrfRefreshPromise = (async () => {
    try {
      const config = await fetchConfig();
      appState.csrfToken = config.csrfToken;
      logger.info('CSRF token refreshed successfully');
      return true;
    } catch (error) {
      logger.error('Failed to refresh CSRF token', error);
      return false;
    } finally {
      csrfRefreshPromise = null;
    }
  })();

  return csrfRefreshPromise;
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
 * Checks if the current viewer is an unauthenticated guest (security is
 * enabled but access has not been granted). Used to suppress login redirects
 * and hide auth-gated UI elements.
 */
export function isGuestMode(): boolean {
  return appState.security.enabled && !appState.security.accessAllowed;
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
 * Gets the species-dictionary dataset version string used as a cache-buster
 * for the per-locale common-name dictionary endpoint.
 * Returns an empty string when the backend has not provided this field
 * (older backend), in which case callers should fetch without a version
 * query parameter.
 *
 * @returns The species dictionary version string, or '' if unavailable
 */
export function getSpeciesDictVersion(): string {
  return appState.speciesDictVersion;
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
