/**
 * MapLibre GL JS Configuration
 *
 * Centralized configuration for map initialization and behavior.
 * This module helps maintain consistency across main and modal maps.
 */

export const MAP_CONFIG = {
  // Zoom levels
  DEFAULT_ZOOM: 12, // Standard zoom for known coordinates
  WORLD_VIEW_ZOOM: 5, // Zoom level for 0,0 coordinates

  // Animation settings (disabled to prevent jump)
  ANIMATION_DURATION: 0, // No animation for position updates
  FADE_DURATION: 0, // No fade animation on load

  // Tile provider
  TILE_URL: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
  TILE_ATTRIBUTION: [
    '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
  ],

  // Map behavior
  SCROLL_ZOOM: false, // Disabled by default, requires Ctrl/Cmd
  KEYBOARD_NAV: true, // Enable keyboard navigation
  PITCH_WITH_ROTATE: false, // Disable 3D rotation
  TOUCH_ZOOM_ROTATE: false, // Disable touch rotation

  // Style configuration
  STYLE_VERSION: 8,
  BACKGROUND_COLOR: '#f0f0f0',
} as const;

/**
 * Create MapLibre style object with OSM tiles
 */
export function createMapStyle() {
  return {
    version: MAP_CONFIG.STYLE_VERSION,
    sources: {
      osm: {
        type: 'raster' as const,
        tiles: [MAP_CONFIG.TILE_URL],
        tileSize: 256,
        attribution: MAP_CONFIG.TILE_ATTRIBUTION.join(' '),
      },
    },
    layers: [
      {
        id: 'osm',
        type: 'raster' as const,
        source: 'osm',
        minzoom: 0,
        maxzoom: 22,
      },
    ],
  };
}

/**
 * Calculate appropriate zoom level based on coordinates
 */
export function getInitialZoom(lat: number, lng: number): number {
  return lat !== 0 || lng !== 0 ? MAP_CONFIG.DEFAULT_ZOOM : MAP_CONFIG.WORLD_VIEW_ZOOM;
}

/**
 * Type guard to check if MapLibre is loaded
 */
export function isMapLibreLoaded(maplibregl: unknown): maplibregl is typeof import('maplibre-gl') {
  return maplibregl !== null && typeof maplibregl === 'object';
}
