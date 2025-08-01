/**
 * auth.ts
 *
 * Authentication state management store for UI security controls and user session state.
 * Manages client-side authentication state and provides logout functionality.
 *
 * IMPORTANT: This store only manages UI state - actual security is enforced server-side.
 * The server validates sessions and permissions; this store just reflects that state.
 *
 * Usage:
 * - Layout components for showing/hiding secured features
 * - Navigation guards for UI elements
 * - Logout functionality across the application
 * - Security-aware component visibility
 *
 * Features:
 * - Client-side login state tracking
 * - Security configuration management
 * - Server-side logout integration
 * - Access control UI state
 *
 * Security Model:
 * - UI state only - not a security boundary
 * - Server enforces actual authentication
 * - Uses session cookies for real auth
 * - CSRF protection on logout
 *
 * State:
 * - isLoggedIn: boolean - User authentication status
 * - security.enabled: boolean - Whether security features are active
 * - security.accessAllowed: boolean - Whether user has access permissions
 */
import { writable } from 'svelte/store';
import { loggers } from '$lib/utils/logger';

const logger = loggers.auth;

interface AuthState {
  isLoggedIn: boolean;
  security: {
    enabled: boolean;
    accessAllowed: boolean;
  };
}

function createAuthStore() {
  const { subscribe, set, update } = writable<AuthState>({
    isLoggedIn: false,
    security: {
      enabled: false,
      accessAllowed: true,
    },
  });

  return {
    subscribe,

    /**
     * Initialize auth state from server configuration
     */
    init: (securityEnabled: boolean, accessAllowed: boolean = true) => {
      logger.info('[AUTH_DEBUG] Initializing auth state', {
        securityEnabled,
        accessAllowed,
        timestamp: new Date().toISOString(),
      });

      update(state => {
        const newState = {
          ...state,
          security: {
            enabled: securityEnabled,
            accessAllowed,
          },
        };

        logger.info('[AUTH_DEBUG] Auth state initialized', {
          previousState: state,
          newState,
          timestamp: new Date().toISOString(),
        });

        return newState;
      });
    },

    /**
     * Set login state
     */
    setLoggedIn: (isLoggedIn: boolean) => {
      logger.info('[AUTH_DEBUG] Setting login state', {
        newLoginState: isLoggedIn,
        timestamp: new Date().toISOString(),
      });

      update(state => {
        const newState = {
          ...state,
          isLoggedIn,
        };

        logger.info('[AUTH_DEBUG] Login state updated', {
          previousLoginState: state.isLoggedIn,
          newLoginState: isLoggedIn,
          fullPreviousState: state,
          fullNewState: newState,
          timestamp: new Date().toISOString(),
        });

        return newState;
      });
    },

    /**
     * Set security configuration
     */
    setSecurity: (enabled: boolean, accessAllowed: boolean) => {
      logger.info('[AUTH_DEBUG] Setting security configuration', {
        enabled,
        accessAllowed,
        timestamp: new Date().toISOString(),
      });

      update(state => {
        const newState = {
          ...state,
          security: {
            enabled,
            accessAllowed,
          },
        };

        logger.info('[AUTH_DEBUG] Security configuration updated', {
          previousSecurity: state.security,
          newSecurity: { enabled, accessAllowed },
          fullPreviousState: state,
          fullNewState: newState,
          timestamp: new Date().toISOString(),
        });

        return newState;
      });
    },

    /**
     * Perform logout operation
     */
    logout: async (): Promise<void> => {
      logger.info('[AUTH_DEBUG] Starting logout process', {
        currentUrl: window.location.href,
        timestamp: new Date().toISOString(),
      });

      try {
        // Use the V1 logout endpoint which works with the OAuth session
        logger.info('[AUTH_DEBUG] Calling logout endpoint', {
          endpoint: '/logout',
          method: 'GET',
          credentials: 'include',
          redirect: 'manual',
        });

        const response = await fetch('/logout', {
          method: 'GET',
          credentials: 'include',
          redirect: 'manual', // Don't follow redirects automatically
        });

        logger.info('[AUTH_DEBUG] Logout response received', {
          status: response.status,
          statusText: response.statusText,
          type: response.type,
          ok: response.ok,
          headers: Object.fromEntries(response.headers.entries()),
          timestamp: new Date().toISOString(),
        });

        // The V1 logout endpoint returns a redirect (302) to / on success
        if (response.type === 'opaqueredirect' || response.status === 302 || response.ok) {
          logger.info('[AUTH_DEBUG] Logout successful, clearing auth state', {
            responseType: response.type,
            responseStatus: response.status,
          });

          // Clear auth state
          const newState = {
            isLoggedIn: false,
            security: {
              enabled: true,
              accessAllowed: false,
            },
          };

          logger.info('[AUTH_DEBUG] Setting logout state', { newState });
          set(newState);

          // Redirect to the Svelte UI root
          logger.info('[AUTH_DEBUG] Redirecting to UI root', {
            redirectUrl: '/ui/',
            currentUrl: window.location.href,
          });
          window.location.href = '/ui/';
        } else {
          const errorMsg = `Logout failed: ${response.statusText}`;
          logger.error('[AUTH_DEBUG] Logout failed', {
            status: response.status,
            statusText: response.statusText,
            type: response.type,
            error: errorMsg,
          });
          throw new Error(errorMsg);
        }
      } catch (error) {
        logger.error('[AUTH_DEBUG] Logout error occurred', {
          error,
          errorMessage: error instanceof Error ? error.message : 'Unknown error',
          timestamp: new Date().toISOString(),
        });
        throw error;
      }
    },

    /**
     * Check if user needs to login
     */
    needsLogin: (state: AuthState): boolean => {
      const needsLogin =
        state.security.enabled && !state.security.accessAllowed && !state.isLoggedIn;

      logger.debug('[AUTH_DEBUG] Checking if user needs login', {
        securityEnabled: state.security.enabled,
        accessAllowed: state.security.accessAllowed,
        isLoggedIn: state.isLoggedIn,
        needsLogin,
        fullState: state,
        timestamp: new Date().toISOString(),
      });

      return needsLogin;
    },
  };
}

export const auth = createAuthStore();
