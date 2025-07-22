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
 * Media player icons for audio controls
 */
export const mediaIcons = {
  play: `<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z"></path>
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z"></path>
  </svg>`,
  
  pause: `<svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M10 9v6m4-6v6m7-3a9 9 0 11-18 0 9 9 0 0118 0z"></path>
  </svg>`,
  
  download: `<svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth="2" d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"></path>
  </svg>`,
  
  volume: `<svg width="16" height="16" viewBox="0 0 24 24" style="color: white;" aria-hidden="true">
    <path d="M12 5v14l-7-7h-3v-4h3l7-7z" fill="currentColor"/>
    <path d="M16 8a4 4 0 0 1 0 8" stroke="currentColor" fill="none" strokeWidth="2" strokeLinecap="round"/>
    <path d="M19 5a8 8 0 0 1 0 14" stroke="currentColor" fill="none" strokeWidth="2" strokeLinecap="round"/>
  </svg>`,
};

/**
 * Additional icon collections can be added here as needed:
 *
 * export const navigationIcons = { ... };
 * export const formIcons = { ... };
 * export const dataIcons = { ... };
 */
