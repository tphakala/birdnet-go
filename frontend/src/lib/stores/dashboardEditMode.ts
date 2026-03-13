import { writable } from 'svelte/store';

/**
 * Shared store for dashboard edit mode state.
 * Used by sidebar to trigger edit mode and by DashboardPage to react to it.
 * A shared store is needed because the SPA router doesn't remount
 * DashboardPage when navigating to the same route with different query params.
 */
export const dashboardEditMode = writable(false);
