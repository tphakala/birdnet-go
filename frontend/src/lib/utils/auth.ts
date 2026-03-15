/**
 * Authentication utilities for feature detection
 *
 * Provides reactive derived stores for auth state that work correctly
 * with Svelte 5's $derived and auto-subscription via $ prefix.
 */

import { auth } from '$lib/stores/auth';
import { derived } from 'svelte/store';

/**
 * Reactive store: true when user has permission to perform reviews.
 * Use as $hasReviewPermission in components.
 */
export const hasReviewPermission = derived(auth, authState => {
  if (!authState.security.enabled) return true;
  return authState.security.accessAllowed;
});

/**
 * Reactive store: true when user is authenticated (or security is disabled).
 * Use as $isAuthenticated in components.
 */
export const isAuthenticated = derived(auth, authState => {
  if (!authState.security.enabled) return true;
  return authState.security.accessAllowed;
});

/**
 * Initialize auth context for components that need review permissions
 * Should be called with server-side props (same pattern as DesktopSidebar)
 */
export function initAuthContext(securityEnabled: boolean, accessAllowed: boolean = true) {
  auth.setSecurity(securityEnabled, accessAllowed);
}
