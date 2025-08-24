export const TestDataManager = {
  async createTestUser(
    options: {
      username?: string;
      role?: 'admin' | 'user';
    } = {}
  ) {
    const response = await fetch('/api/v2/test/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: options.username ?? 'testuser',
        password: 'testpassword',
        role: options.role ?? 'user',
      }),
    });

    return response.json();
  },

  async generateDetections(count: number = 10) {
    const species = ['American Robin', 'Blue Jay', 'Cardinal', 'Sparrow'];
    const detections = [];

    for (let i = 0; i < count; i++) {
      detections.push({
        species: species[Math.floor(Math.random() * species.length)],
        confidence: Math.random() * 0.3 + 0.7, // 0.7 - 1.0
        timestamp: new Date(Date.now() - i * 60000).toISOString(),
        location: { lat: 40.7128, lng: -74.006 },
      });
    }

    await fetch('/api/v2/test/detections', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ detections }),
    });

    return detections;
  },

  async setupAudioSource(type: 'microphone' | 'file' | 'rtsp') {
    await fetch('/api/v2/test/audio-source', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ type, config: {} }),
    });
  },

  async cleanup() {
    await fetch('/api/v2/test/cleanup', { method: 'POST' });
  },
};
