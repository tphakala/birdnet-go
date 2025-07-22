/**
 * Centralized icon path definitions for SVG icons used across the application.
 * This module provides a single source of truth for icon paths, improving
 * maintainability and reusability.
 */

export type AlertIconType = 'error' | 'warning' | 'info' | 'success';

/**
 * SVG path definitions for alert/notification icons.
 * These paths are designed for 24x24 viewBox icons.
 */
export const alertIcons: Record<AlertIconType, string> = {
  error: 'M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z',
  warning:
    'M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z',
  info: 'M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z',
  success: 'M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z',
};

/**
 * Additional icon collections can be added here as needed:
 *
 * export const navigationIcons = { ... };
 * export const formIcons = { ... };
 * export const dataIcons = { ... };
 */
