/**
 * Basic API Integration Tests
 *
 * Validates connectivity and basic functionality of the BirdNET-Go API.
 * These tests run in a real browser against a real backend.
 *
 * Usage:
 *   1. Start backend: task integration-backend (in separate terminal)
 *   2. Run tests: npm run test:integration -- --run
 */

import { describe, expect, it } from 'vitest';
import { apiCall, API_BASE } from './integration-setup';

describe('API Health', () => {
  it('health endpoint responds with healthy status', async () => {
    const response = await fetch(`${API_BASE}/health`);

    expect(response.ok).toBe(true);
    expect(response.status).toBe(200);

    const data = await response.json();
    expect(data).toHaveProperty('status');
    expect(data.status).toBe('healthy');
  });

  it('health response includes version info', async () => {
    const response = await fetch(`${API_BASE}/health`);
    const data = await response.json();

    expect(data).toHaveProperty('version');
    expect(data).toHaveProperty('uptime');
  });
});

describe('Settings API', () => {
  it('can fetch settings', async () => {
    const response = await apiCall('/settings');

    expect(response.ok).toBe(true);

    const settings = await response.json();
    expect(settings).toBeDefined();
    // Settings should have these top-level keys
    expect(settings).toHaveProperty('main');
    expect(settings).toHaveProperty('birdnet');
    expect(settings).toHaveProperty('realtime');
  });
});

describe('Detections API', () => {
  it('can fetch detections with pagination', async () => {
    const response = await apiCall('/detections?limit=5');

    expect(response.ok).toBe(true);

    const result = await response.json();
    expect(result).toBeDefined();
    // Detections endpoint returns paginated response
    expect(result).toHaveProperty('data');
    expect(result).toHaveProperty('total');
    expect(result).toHaveProperty('limit');
    expect(Array.isArray(result.data)).toBe(true);
  });
});

describe('API Headers', () => {
  it('responses include proper content-type', async () => {
    const response = await fetch(`${API_BASE}/health`);

    const contentType = response.headers.get('content-type');
    expect(contentType).toContain('application/json');
  });
});
