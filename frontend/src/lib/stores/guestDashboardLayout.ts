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
import { getStoredValue, setStoredValue, removeStoredValue } from '$lib/utils/storage';

/** localStorage key for the guest dashboard layout */
export const GUEST_LAYOUT_KEY = 'birdnet-go:guest-dashboard-layout';

function isValidLayout(v: unknown): v is DashboardLayout {
  return (
    typeof v === 'object' &&
    v !== null &&
    'elements' in v &&
    Array.isArray((v as DashboardLayout).elements)
  );
}

/**
 * Reactive Svelte store for the guest dashboard layout.
 * Hydrated from localStorage on creation; subscribers are notified on changes.
 */
export const guestDashboardLayout = writable<DashboardLayout | null>(
  getStoredValue<DashboardLayout | null>(GUEST_LAYOUT_KEY, null, isValidLayout)
);

/**
 * Saves a layout for guest users.
 * Writes to both the Svelte store (for reactive UI updates) and localStorage
 * (for persistence across page reloads).
 */
export function saveGuestLayout(layout: DashboardLayout): void {
  guestDashboardLayout.set(layout);
  setStoredValue(GUEST_LAYOUT_KEY, layout);
}

/**
 * Clears the guest dashboard layout.
 * Removes from both the Svelte store and localStorage.
 * Used when resetting to defaults.
 */
export function clearGuestLayout(): void {
  guestDashboardLayout.set(null);
  removeStoredValue(GUEST_LAYOUT_KEY);
}
