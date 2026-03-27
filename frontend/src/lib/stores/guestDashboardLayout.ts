/**
 * guestDashboardLayout.ts
 *
 * Reactive store for persisting dashboard layout customizations for guest
 * (unauthenticated) users via localStorage.
 *
 * Authenticated users have their layout stored server-side in settings.
 * Guests cannot write to the settings API, so we fall back to localStorage
 * to persist their layout preferences across page reloads.
 *
 * The store stays in sync with localStorage: saves write through to
 * localStorage, and the initial value is hydrated from localStorage.
 */
import type { DashboardLayout } from './settings';
import { writable } from 'svelte/store';
import { getLogger } from '$lib/utils/logger';

const logger = getLogger('dashboard');

/** localStorage key for the guest dashboard layout */
export const GUEST_LAYOUT_KEY = 'birdnet-go:guest-dashboard-layout';

/**
 * Reads the guest dashboard layout from localStorage.
 * Returns null if no layout is stored or if the stored value is invalid.
 */
function readFromStorage(): DashboardLayout | null {
  if (typeof window === 'undefined') return null;

  try {
    const raw = localStorage.getItem(GUEST_LAYOUT_KEY);
    if (!raw) return null;

    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === 'object' &&
      parsed !== null &&
      'elements' in parsed &&
      Array.isArray((parsed as DashboardLayout).elements)
    ) {
      return parsed as DashboardLayout;
    }

    // Stored data doesn't look like a valid layout - remove it
    localStorage.removeItem(GUEST_LAYOUT_KEY);
    return null;
  } catch (error) {
    logger.warn('Failed to parse guest dashboard layout from localStorage:', error);
    return null;
  }
}

/**
 * Reactive Svelte store for the guest dashboard layout.
 * Hydrated from localStorage on creation; subscribers are notified on changes.
 */
export const guestDashboardLayout = writable<DashboardLayout | null>(readFromStorage());

/**
 * Saves a layout for guest users.
 * Writes to both the Svelte store (for reactive UI updates) and localStorage
 * (for persistence across page reloads).
 */
export function saveGuestLayout(layout: DashboardLayout): void {
  guestDashboardLayout.set(layout);

  if (typeof window === 'undefined') return;
  try {
    localStorage.setItem(GUEST_LAYOUT_KEY, JSON.stringify(layout));
  } catch (error) {
    logger.warn('Failed to save guest dashboard layout to localStorage:', error);
  }
}

/**
 * Clears the guest dashboard layout.
 * Removes from both the Svelte store and localStorage.
 * Used when resetting to defaults.
 */
export function clearGuestLayout(): void {
  guestDashboardLayout.set(null);

  if (typeof window === 'undefined') return;
  try {
    localStorage.removeItem(GUEST_LAYOUT_KEY);
  } catch (error) {
    logger.warn('Failed to clear guest dashboard layout from localStorage:', error);
  }
}
