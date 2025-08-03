/**
 * Authentication utilities for feature detection
 *
 * Uses the existing authentication pattern from DesktopSidebar.svelte
 * Integrates with the established server-side props pattern
 */

import { loggers } from '$lib/utils/logger';
import { auth } from '$lib/stores/auth';
import { get } from 'svelte/store';

const logger = loggers.auth;

/**
 * Check if user has permission to perform reviews
 * Based on existing authentication pattern in the app
 */
export function hasReviewPermission(): boolean {
  try {
    const authState = get(auth);

    // If security is disabled, allow all actions
    if (!authState.security.enabled) {
      logger.debug('Security disabled, allowing review permission');
      return true;
    }

    // If security is enabled, user must have access
    const hasAccess = authState.security.accessAllowed;
    logger.debug('Review permission check:', {
      securityEnabled: authState.security.enabled,
      accessAllowed: hasAccess,
    });

    return hasAccess;
  } catch (error) {
    logger.error('Error checking review permission:', error);
    return false; // Fail closed for security
  }
}

/**
 * Check if user is authenticated
 * Uses the existing auth store pattern
 */
export function isAuthenticated(): boolean {
  try {
    const authState = get(auth);

    // If security is disabled, consider user as "authenticated" for UI purposes
    if (!authState.security.enabled) {
      return true;
    }

    return authState.security.accessAllowed;
  } catch (error) {
    logger.error('Error checking authentication:', error);
    return false; // Fail closed for security
  }
}

/**
 * Initialize auth context for components that need review permissions
 * Should be called with server-side props (same pattern as DesktopSidebar)
 */
export function initAuthContext(securityEnabled: boolean, accessAllowed: boolean = true) {
  auth.setSecurity(securityEnabled, accessAllowed);
}
