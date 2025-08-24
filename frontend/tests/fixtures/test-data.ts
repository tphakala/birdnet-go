import type { Page, Route } from '@playwright/test';

export const TestDataManager = {
  async createTestUser(
    options: {
      username?: string;
      role?: 'admin' | 'user';
    } = {}
  ) {
    try {
      const response = await fetch('/api/v2/test/users', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          username: options.username ?? 'testuser',
          password: 'testpassword',
          role: options.role ?? 'user',
        }),
      });

      if (response.ok) {
        return response.json();
      } else {
        // Fallback for when test endpoints don't exist
        // eslint-disable-next-line no-console -- Test debugging information
        console.warn('Test user creation endpoint not available, using mock data');
        return {
          id: 'test-user-1',
          username: options.username ?? 'testuser',
          role: options.role ?? 'user',
        };
      }
    } catch (error) {
      // Mock response when endpoints don't exist
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available, using mock data:', error);
      return {
        id: 'test-user-1',
        username: options.username ?? 'testuser',
        role: options.role ?? 'user',
      };
    }
  },

  async generateDetections(count: number = 10) {
    const species = ['American Robin', 'Blue Jay', 'Cardinal', 'Sparrow'];
    const detections = [];

    for (let i = 0; i < count; i++) {
      detections.push({
        id: `detection-${i}`,
        species: species[Math.floor(Math.random() * species.length)],
        confidence: Math.random() * 0.3 + 0.7, // 0.7 - 1.0
        timestamp: new Date(Date.now() - i * 60000).toISOString(),
        location: { lat: 40.7128, lng: -74.006 },
      });
    }

    try {
      const response = await fetch('/api/v2/test/detections', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ detections }),
      });

      if (!response.ok) {
        // eslint-disable-next-line no-console -- Test debugging information
        console.warn('Test detections endpoint not available, using mock data only');
      }
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available for detections:', error);
    }

    return detections;
  },

  async setupAudioSource(type: 'microphone' | 'file' | 'rtsp') {
    try {
      const response = await fetch('/api/v2/test/audio-source', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type, config: {} }),
      });

      if (!response.ok) {
        // eslint-disable-next-line no-console -- Test debugging information
        console.warn('Test audio source endpoint not available, using mock setup');
      }
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available for audio source setup:', error);
    }
  },

  async cleanup() {
    try {
      const response = await fetch('/api/v2/test/cleanup', { method: 'POST' });
      if (!response.ok) {
        // eslint-disable-next-line no-console -- Test debugging information
        console.warn('Test cleanup endpoint not available');
      }
    } catch (error) {
      // eslint-disable-next-line no-console -- Test debugging information
      console.warn('Test API not available for cleanup:', error);
    }
  },

  // Mock API endpoints for testing when backend isn't available
  setupMockEndpoints(page: Page) {
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
          body: JSON.stringify({ success: true }),
        });
      } else if (url.includes('/test/cleanup')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: true }),
        });
      } else if (url.includes('/test/audio-source')) {
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ success: true }),
        });
      } else {
        route.fulfill({
          status: 404,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'Mock endpoint not implemented' }),
        });
      }
    });
  },
};
