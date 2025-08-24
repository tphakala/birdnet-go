import type { Page, Route } from '@playwright/test';

// Helper to build absolute URLs for API calls
function getApiUrl(path: string): string {
  const baseUrl =
    process.env['TEST_BASE_URL'] ?? process.env['BASE_URL'] ?? 'http://localhost:8080';
  return new URL(path, baseUrl).toString();
}

// Helper for API calls with proper error handling
async function apiCall<T>(path: string, options: RequestInit = {}): Promise<T> {
  const url = getApiUrl(path);

  try {
    const response = await fetch(url, {
      ...options,
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(`API call failed: ${response.status} ${response.statusText} - ${errorText}`);
    }

    return (await response.json()) as T;
  } catch (error) {
    if (error instanceof TypeError && error.message.includes('fetch')) {
      throw new Error(`Network error calling ${url}: ${error.message}`);
    }
    throw error;
  }
}

export const TestDataManager = {
  async createTestUser(
    options: {
      username?: string;
      role?: 'admin' | 'user';
    } = {}
  ): Promise<{ id: string; username: string; role: string }> {
    try {
      return await apiCall<{ id: string; username: string; role: string }>('/api/v2/test/users', {
        method: 'POST',
        body: JSON.stringify({
          username: options.username ?? 'testuser',
          password: 'testpassword',
          role: options.role ?? 'user',
        }),
      });
    } catch (error) {
      // Fallback for when test endpoints don't exist
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test user creation endpoint not available, using mock data:', error);
      return {
        id: 'test-user-1',
        username: options.username ?? 'testuser',
        role: options.role ?? 'user',
      };
    }
  },

  async createTestDetections(detections: unknown[]): Promise<unknown[]> {
    try {
      await apiCall('/api/v2/test/detections', {
        method: 'POST',
        body: JSON.stringify({ detections }),
      });
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test detections endpoint not available, using mock data only:', error);
    }

    return detections;
  },

  async setupAudioSource(type: 'microphone' | 'file' | 'rtsp'): Promise<void> {
    try {
      await apiCall('/api/v2/test/audio-source', {
        method: 'POST',
        body: JSON.stringify({ type, config: {} }),
      });
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test audio source endpoint not available, using mock setup:', error);
    }
  },

  async cleanup(): Promise<void> {
    try {
      await apiCall('/api/v2/test/cleanup', { method: 'POST' });
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available for cleanup:', error);
    }
  },

  // Mock API endpoints for testing when backend isn't available
  setupMockEndpoints(page: Page): void {
    // Mock test endpoints that don't exist yet
    page.route('/api/v2/test/**', (route: Route) => {
      const url = route.request().url();

      if (url.includes('/test/users')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ id: 'test-user', username: 'testuser', role: 'user' }),
        });
      } else if (url.includes('/test/detections')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'Test detections created' }),
        });
      } else if (url.includes('/test/audio-source')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'Audio source configured' }),
        });
      } else if (url.includes('/test/cleanup')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'Test data cleaned up' }),
        });
      } else {
        route.fulfill({
          status: 404,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Test endpoint not found' }),
        });
      }
    });

    // Mock health endpoint if needed
    page.route('/api/v2/health', (route: Route) => {
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'ok',
          timestamp: new Date().toISOString(),
          version: 'test',
        }),
      });
    });
  },
};
