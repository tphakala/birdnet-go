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

const logger = loggers.store;

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
      update(state => ({
        ...state,
        security: {
          enabled: securityEnabled,
          accessAllowed,
        },
      }));
    },

    /**
     * Set login state
     */
    setLoggedIn: (isLoggedIn: boolean) => {
      update(state => ({
        ...state,
        isLoggedIn,
      }));
    },

    /**
     * Set security configuration
     */
    setSecurity: (enabled: boolean, accessAllowed: boolean) => {
      update(state => ({
        ...state,
        security: {
          enabled,
          accessAllowed,
        },
      }));
    },

    /**
     * Perform logout operation
     */
    logout: async (): Promise<void> => {
      try {
        const response = await fetch('/api/logout', {
          method: 'POST',
          credentials: 'include',
        });

        if (!response.ok) {
          throw new Error(`Logout failed: ${response.statusText}`);
        }

        // Clear auth state
        set({
          isLoggedIn: false,
          security: {
            enabled: true,
            accessAllowed: false,
          },
        });

        // Redirect to login page
        window.location.href = '/login';
      } catch (error) {
         
        logger.error('Logout error:', error);
        throw error;
      }
    },

    /**
     * Check if user needs to login
     */
    needsLogin: (state: AuthState): boolean => {
      return state.security.enabled && !state.security.accessAllowed && !state.isLoggedIn;
    },
  };
}

export const auth = createAuthStore();
