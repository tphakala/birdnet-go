/**
 * Integration Test Setup (Browser Mode)
 *
 * This setup file configures tests to run in a real browser against a real BirdNET-Go backend.
 * Unlike unit tests which mock API calls in jsdom, integration tests use real browser APIs
 * and make real HTTP requests through the Vite proxy.
 *
 * Prerequisites:
 *   - Backend running on http://localhost:8080
 *   - Start with: task integration-backend
 *
 * Usage:
 *   - Name test files: *.integration.test.ts
 *   - Run with: npm run test:integration
 */

import { beforeEach, afterAll } from 'vitest';

// API base path (uses Vite proxy in browser mode)
export const API_BASE = '/api/v2';

beforeEach(() => {
  // Clear any local storage between tests
  if (typeof localStorage !== 'undefined') {
    localStorage.clear();
  }
  if (typeof sessionStorage !== 'undefined') {
    sessionStorage.clear();
  }
});

afterAll(() => {
  // Cleanup
});

/**
 * Helper to make API calls with proper headers
 */
export async function apiCall(endpoint: string, options: RequestInit = {}): Promise<Response> {
  const url = endpoint.startsWith('/api') ? endpoint : `${API_BASE}${endpoint}`;

  // Get CSRF token from the backend first if needed for mutations
  let csrfToken = '';
  if (options.method && ['POST', 'PUT', 'DELETE', 'PATCH'].includes(options.method.toUpperCase())) {
    try {
      const tokenResponse = await fetch(`${API_BASE}/auth/csrf`);
      if (tokenResponse.ok) {
        const tokenData = await tokenResponse.json();
        csrfToken = tokenData.token ?? '';
      }
    } catch {
      // No CSRF token needed or auth not configured
    }
  }

  const headers = new Headers(options.headers);
  if (csrfToken) {
    headers.set('X-CSRF-Token', csrfToken);
  }
  if (!headers.has('Content-Type') && options.body) {
    headers.set('Content-Type', 'application/json');
  }

  return fetch(url, {
    ...options,
    headers,
  });
}

/**
 * Test utilities for integration tests
 */
export const integrationUtils = {
  /**
   * Get real settings from backend
   */
  getSettings: async () => {
    const response = await apiCall('/settings');
    if (!response.ok) {
      throw new Error(`Failed to get settings: ${response.status}`);
    }
    return response.json();
  },

  /**
   * Get detections from backend
   */
  getDetections: async (params?: { limit?: number; offset?: number }) => {
    const searchParams = new URLSearchParams();
    if (params?.limit) searchParams.set('limit', String(params.limit));
    if (params?.offset) searchParams.set('offset', String(params.offset));

    const url = `/detections${searchParams.toString() ? '?' + searchParams.toString() : ''}`;
    const response = await apiCall(url);
    if (!response.ok) {
      throw new Error(`Failed to get detections: ${response.status}`);
    }
    return response.json();
  },

  /**
   * Get system status from backend
   */
  getSystemStatus: async () => {
    const response = await apiCall('/status');
    if (!response.ok) {
      throw new Error(`Failed to get status: ${response.status}`);
    }
    return response.json();
  },

  /**
   * Wait for an element to appear in the DOM
   */
  waitForElement: async (selector: string, timeout = 5000): Promise<Element | null> => {
    const startTime = Date.now();
    while (Date.now() - startTime < timeout) {
      const element = document.querySelector(selector);
      if (element) return element;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    return null;
  },

  /**
   * Wait for text to appear in the document
   */
  waitForText: async (text: string, timeout = 5000): Promise<boolean> => {
    const startTime = Date.now();
    while (Date.now() - startTime < timeout) {
      if (document.body.textContent.includes(text)) return true;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    return false;
  },
};
