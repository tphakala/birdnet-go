/**
 * API Integration Tests
 *
 * Validates connectivity and functionality of the BirdNET-Go API.
 * These tests run in a real browser against a real backend.
 *
 * Usage:
 *   1. Start backend: task integration-backend (in separate terminal)
 *   2. Run tests: npm run test:integration -- --run
 */

import { describe, expect, it } from 'vitest';
import { apiCall, API_BASE } from './integration-setup';

// ============================================================================
// Health API
// ============================================================================

describe('Health API', () => {
  it('health endpoint responds with healthy status', async () => {
    const response = await fetch(`${API_BASE}/health`);

    expect(response.ok).toBe(true);
    expect(response.status).toBe(200);

    const data = await response.json();
    expect(data).toHaveProperty('status');
    expect(data.status).toBe('healthy');
  });

  it('health response includes version and uptime', async () => {
    const response = await fetch(`${API_BASE}/health`);
    const data = await response.json();

    expect(data).toHaveProperty('version');
    expect(data).toHaveProperty('uptime');
    expect(data).toHaveProperty('uptime_seconds');
    expect(typeof data.uptime_seconds).toBe('number');
  });

  it('health response includes database status', async () => {
    const response = await fetch(`${API_BASE}/health`);
    const data = await response.json();

    expect(data).toHaveProperty('database_status');
    expect(data.database_status).toBe('connected');
  });

  it('health response includes system metrics', async () => {
    const response = await fetch(`${API_BASE}/health`);
    const data = await response.json();

    expect(data).toHaveProperty('system');
    expect(data.system).toHaveProperty('cpu_usage');
    expect(data.system).toHaveProperty('memory');
  });
});

// ============================================================================
// System API
// ============================================================================

describe('System API', () => {
  it('can fetch system info', async () => {
    const response = await apiCall('/system/info');

    expect(response.ok).toBe(true);

    const info = await response.json();
    expect(info).toHaveProperty('hostname');
    expect(info).toHaveProperty('os_display');
    expect(info).toHaveProperty('architecture');
  });

  it('can fetch database stats', async () => {
    const response = await apiCall('/system/database/stats');

    expect(response.ok).toBe(true);

    const stats = await response.json();
    expect(stats).toHaveProperty('type');
    expect(stats).toHaveProperty('total_detections');
    expect(stats).toHaveProperty('connected');
    expect(stats.connected).toBe(true);
  });

  it('can fetch resource info', async () => {
    const response = await apiCall('/system/resources');

    expect(response.ok).toBe(true);

    const resources = await response.json();
    expect(resources).toBeDefined();
  });

  it('can fetch disk info', async () => {
    const response = await apiCall('/system/disks');

    expect(response.ok).toBe(true);

    const disks = await response.json();
    expect(disks).toBeDefined();
  });

  it('can fetch job queue stats', async () => {
    const response = await apiCall('/system/jobs');

    // Jobs endpoint may return 500 if processor is not available
    expect([200, 500]).toContain(response.status);

    if (response.ok) {
      const jobs = await response.json();
      expect(jobs).toBeDefined();
    }
  });

  it('can fetch audio devices', async () => {
    const response = await apiCall('/system/audio/devices');

    expect(response.ok).toBe(true);

    const devices = await response.json();
    expect(Array.isArray(devices)).toBe(true);
  });

  it('can fetch CPU temperature', async () => {
    const response = await apiCall('/system/temperature/cpu');

    // CPU temp might not be available on all systems
    expect([200, 404, 500]).toContain(response.status);
  });
});

// ============================================================================
// App Configuration
// ============================================================================

describe('App Config API', () => {
  it('can fetch app configuration', async () => {
    const response = await apiCall('/app/config');

    expect(response.ok).toBe(true);

    const config = await response.json();
    expect(config).toBeDefined();
  });

  it('can fetch available locales', async () => {
    const response = await apiCall('/settings/locales');

    expect(response.ok).toBe(true);

    const locales = await response.json();
    expect(locales).toBeDefined();
    expect(typeof locales).toBe('object');
  });
});

// ============================================================================
// Settings
// ============================================================================

describe('Settings API', () => {
  it('can fetch settings', async () => {
    const response = await apiCall('/settings');

    expect(response.ok).toBe(true);

    const settings = await response.json();
    expect(settings).toBeDefined();
    expect(settings).toHaveProperty('main');
    expect(settings).toHaveProperty('birdnet');
    expect(settings).toHaveProperty('realtime');
  });

  it('settings contain expected nested structure', async () => {
    const response = await apiCall('/settings');
    const settings = await response.json();

    // Check main settings structure
    expect(settings.main).toHaveProperty('name');

    // Check birdnet settings
    expect(settings.birdnet).toHaveProperty('sensitivity');
    expect(settings.birdnet).toHaveProperty('threshold');
  });
});

// ============================================================================
// Detections
// ============================================================================

describe('Detections API', () => {
  it('can fetch detections with pagination', async () => {
    const response = await apiCall('/detections?limit=5');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toHaveProperty('data');
    expect(result).toHaveProperty('total');
    expect(result).toHaveProperty('limit');
    expect(Array.isArray(result.data)).toBe(true);
  });

  it('returns paginated results with limit', async () => {
    const response = await apiCall('/detections?limit=3');
    const result = await response.json();

    // API has a default limit, verify structure
    expect(result).toHaveProperty('limit');
    expect(typeof result.limit).toBe('number');
    expect(result.data.length).toBeLessThanOrEqual(result.limit);
  });

  it('can fetch recent detections', async () => {
    const response = await apiCall('/detections/recent');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toBeDefined();
  });

  it('can fetch detections for specific date', async () => {
    const today = new Date().toISOString().split('T')[0];
    const response = await apiCall(`/detections/daily/${today}`);

    // May return 200 or 404 depending on data availability
    expect([200, 404]).toContain(response.status);
  });
});

// ============================================================================
// Analytics
// ============================================================================

describe('Analytics API', () => {
  it('can fetch species summary', async () => {
    const response = await apiCall('/analytics/species/summary');

    expect(response.ok).toBe(true);

    const summary = await response.json();
    expect(summary).toBeDefined();
  });

  it('can fetch daily species summary', async () => {
    const today = new Date().toISOString().split('T')[0];
    const response = await apiCall(`/analytics/species/daily?date=${today}`);

    expect(response.ok).toBe(true);

    const daily = await response.json();
    expect(daily).toBeDefined();
  });

  it('can fetch time of day distribution', async () => {
    const response = await apiCall('/analytics/time/distribution/hourly');

    expect(response.ok).toBe(true);

    const distribution = await response.json();
    expect(distribution).toBeDefined();
  });

  it('can fetch new species detections', async () => {
    const response = await apiCall('/analytics/species/detections/new');

    expect(response.ok).toBe(true);

    const newSpecies = await response.json();
    expect(newSpecies).toBeDefined();
  });
});

// ============================================================================
// Weather
// ============================================================================

describe('Weather API', () => {
  it('can fetch sun times for today', async () => {
    const today = new Date().toISOString().split('T')[0];
    const response = await apiCall(`/sun/${today}`);

    // Sun endpoint should work if location is configured
    expect([200, 400, 404, 500]).toContain(response.status);

    if (response.ok) {
      const sun = await response.json();
      expect(sun).toBeDefined();
    }
  });

  it('can fetch latest weather', async () => {
    const response = await apiCall('/weather/latest');

    // Weather provider might not be configured
    expect([200, 404, 500]).toContain(response.status);
  });
});

// ============================================================================
// Notifications
// ============================================================================

describe('Notifications API', () => {
  it('can fetch unread notification count', async () => {
    const response = await apiCall('/notifications/unread/count');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toHaveProperty('unreadCount');
    expect(typeof result.unreadCount).toBe('number');
  });
});

// ============================================================================
// Dynamic Thresholds
// ============================================================================

describe('Dynamic Thresholds API', () => {
  it('can fetch dynamic thresholds', async () => {
    const response = await apiCall('/dynamic-thresholds');

    expect(response.ok).toBe(true);

    const thresholds = await response.json();
    expect(thresholds).toBeDefined();
  });

  it('can fetch dynamic threshold stats', async () => {
    const response = await apiCall('/dynamic-thresholds/stats');

    expect(response.ok).toBe(true);

    const stats = await response.json();
    expect(stats).toBeDefined();
  });
});

// ============================================================================
// Range Filter
// ============================================================================

describe('Range Filter API', () => {
  it('can fetch species count in range', async () => {
    const response = await apiCall('/range/species/count');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toBeDefined();
  });

  it('can fetch species list in range', async () => {
    const response = await apiCall('/range/species/list');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toBeDefined();
  });
});

// ============================================================================
// Integrations
// ============================================================================

describe('Integrations API', () => {
  it('can fetch MQTT status', async () => {
    const response = await apiCall('/integrations/mqtt/status');

    expect(response.ok).toBe(true);

    const status = await response.json();
    expect(status).toBeDefined();
  });

  it('can fetch BirdWeather status', async () => {
    const response = await apiCall('/integrations/birdweather/status');

    expect(response.ok).toBe(true);

    const status = await response.json();
    expect(status).toBeDefined();
  });
});

// ============================================================================
// Media
// ============================================================================

describe('Media API', () => {
  it('media audio endpoint validates input', async () => {
    const response = await apiCall('/media/audio');

    // Should return 400 (missing filename) or 404, not 500
    expect([200, 400, 404]).toContain(response.status);
  });
});

// ============================================================================
// Streams
// ============================================================================

describe('Streams API', () => {
  it('can fetch stream status', async () => {
    const response = await apiCall('/streams/status');

    expect(response.ok).toBe(true);

    const status = await response.json();
    expect(status).toBeDefined();
  });

  it('can fetch stream health', async () => {
    const response = await apiCall('/streams/health');

    expect(response.ok).toBe(true);

    const health = await response.json();
    expect(health).toBeDefined();
  });
});

// ============================================================================
// Control & SSE
// ============================================================================

describe('SSE API', () => {
  it('can fetch SSE status', async () => {
    const response = await apiCall('/sse/status');

    expect(response.ok).toBe(true);

    const status = await response.json();
    expect(status).toBeDefined();
  });
});

// ============================================================================
// API Headers & Error Handling
// ============================================================================

describe('API Headers', () => {
  it('responses include proper content-type', async () => {
    const response = await fetch(`${API_BASE}/health`);

    const contentType = response.headers.get('content-type');
    expect(contentType).toContain('application/json');
  });

  it('handles non-existent endpoints gracefully', async () => {
    const response = await apiCall('/nonexistent-endpoint-12345');

    expect(response.status).toBe(404);
  });
});
