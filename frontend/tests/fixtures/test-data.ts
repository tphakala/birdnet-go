import type { Page, Route } from '@playwright/test';

// Detection type for strongly typed test data
export interface Detection {
  id: string;
  species: string;
  confidence: number;
  timestamp: string;
  duration: number;
}

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

    // Handle 204 No Content responses
    if (response.status === 204) {
      return undefined as T;
    }

    // Check content-type before parsing JSON
    const contentType = response.headers.get('content-type');
    if (!contentType?.includes('application/json')) {
      const responseText = await response.text();
      return (responseText === '' ? undefined : responseText) as T;
    }

    return (await response.json()) as T;
  } catch (error) {
    if (error instanceof TypeError && error.message.includes('fetch')) {
      throw new Error(`Network error calling ${url}: ${error.message}`);
    }
    throw error;
  }
}

// Helper for POST requests with JSON body
async function postJSON<T>(path: string, body: unknown): Promise<T> {
  return apiCall<T>(path, {
    method: 'POST',
    body: JSON.stringify(body),
  });
}

// Simple seeded PRNG using mulberry32 algorithm
function seededRandom(seed: number): () => number {
  return function () {
    let t = (seed += 0x6d2b79f5);
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Test data manager for E2E tests
 *
 * Provides utilities for creating test data with graceful fallbacks
 * when test API endpoints are not available.
 */
export const TestDataManager = {
  /**
   * Create a test user
   * @param options User creation options
   * @returns Promise resolving to user data
   */
  async createTestUser(
    options: {
      username?: string;
      role?: 'admin' | 'user';
    } = {}
  ): Promise<{ id: string; username: string; role: 'admin' | 'user' }> {
    try {
      return await postJSON<{ id: string; username: string; role: 'admin' | 'user' }>(
        '/api/v2/test/users',
        {
          username: options.username ?? 'testuser',
          password: 'testpassword',
          role: options.role ?? 'user',
        }
      );
    } catch (error) {
      // Fallback for when test endpoints don't exist
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test user creation endpoint not available, using mock data:', error);
      return {
        id: 'test-user-1',
        username: options.username ?? 'testuser',
        role: options.role ?? ('user' as 'admin' | 'user'),
      };
    }
  },

  /**
   * Generate deterministic test detections using seeded randomization
   * @param count Number of detections to generate
   * @param seed Random seed for reproducible results
   * @param baseTime Base timestamp for detections (defaults to fixed time)
   * @returns Array of detection objects
   */
  generateDetections(
    count = 10,
    seed = 12345,
    baseTime = new Date('2024-01-01T10:00:00Z').getTime()
  ): Detection[] {
    const random = seededRandom(seed);
    const species = ['Robin', 'Sparrow', 'Blue Jay', 'Cardinal', 'Woodpecker', 'Crow', 'Finch'];

    return Array.from({ length: count }, (_, i) => ({
      id: `detection-${i + 1}`,
      species: species[Math.floor(random() * species.length)],
      confidence: Math.round((0.5 + random() * 0.5) * 100) / 100, // 0.5-1.0 range
      timestamp: new Date(baseTime - i * 60000).toISOString(), // 1 minute intervals going back
      duration: Math.round((1 + random() * 4) * 1000), // 1-5 second duration in ms
    }));
  },

  /**
   * Create test detections
   * @param detections Array of detection objects
   * @returns Promise resolving to the detections array
   */
  async createTestDetections(detections: Detection[]): Promise<Detection[]> {
    try {
      await postJSON<{ message: string }>('/api/v2/test/detections', { detections });
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test detections endpoint not available, using mock data only:', error);
    }

    return detections;
  },

  /**
   * Setup audio source for testing
   * @param type Type of audio source to configure
   */
  async setupAudioSource(type: 'microphone' | 'file' | 'rtsp'): Promise<void> {
    try {
      await postJSON<{ message: string }>('/api/v2/test/audio-source', { type, config: {} });
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test audio source endpoint not available, using mock setup:', error);
    }
  },

  /**
   * Clean up test data
   */
  async cleanup(): Promise<void> {
    try {
      await postJSON<{ message: string }>('/api/v2/test/cleanup', {});
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available for cleanup:', error);
    }
  },

  /**
   * Mock API endpoints for testing when backend isn't available
   * @param page Playwright page instance
   */
  setupMockEndpoints(page: Page): void {
    // Mock test endpoints that don't exist yet
    page.route('**/api/v2/test/**', (route: Route) => {
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
    page.route('**/api/v2/health', (route: Route) => {
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
