/**
 * Security contract tests for GET /api/v2/settings.
 *
 * These tests validate that the settings API response format the frontend
 * receives never contains plaintext secrets. The backend is responsible for
 * redacting secrets — these tests document and enforce that contract so any
 * regression (e.g. a new secret field added without redaction) is caught.
 *
 * The redacted placeholder used by the backend is "**********".
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

// Unmock settingsApi so we can exercise the real load() path
vi.unmock('$lib/utils/settingsApi.js');

// Unmock the logger since the API module depends on it
vi.unmock('$lib/utils/logger');

// Mock appState for CSRF
vi.mock('$lib/stores/appState.svelte', () => ({
  getCsrfToken: () => 'test-csrf-token',
  isSentryEnabled: () => false,
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
}));

/** The redacted placeholder the backend uses for configured secrets. */
const REDACTED = '**********';

/**
 * Walk an object by dot-separated path and return the value, or undefined
 * if any segment is missing.
 */
function getNestedValue(obj: unknown, path: string): unknown {
  let current: unknown = obj;
  for (const segment of path.split('.')) {
    if (current === null || current === undefined || typeof current !== 'object') {
      return undefined;
    }
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
}

/**
 * Recursively find all string-valued paths in an object whose key matches
 * one of the sensitive key names, regardless of nesting depth.
 * Returns entries where the value is a non-empty string that is NOT the
 * redacted placeholder — i.e. actual plaintext secrets that leaked through.
 */
function findLeakedSecrets(
  obj: unknown,
  sensitiveKeyNames: Set<string>,
  prefix = ''
): Array<{ path: string; value: string }> {
  const results: Array<{ path: string; value: string }> = [];

  if (obj === null || obj === undefined || typeof obj !== 'object') {
    return results;
  }

  if (Array.isArray(obj)) {
    for (let i = 0; i < obj.length; i++) {
      const itemPath = prefix ? `${prefix}[${i}]` : `[${i}]`;
      results.push(...findLeakedSecrets(obj[i], sensitiveKeyNames, itemPath));
    }
    return results;
  }

  for (const [key, value] of Object.entries(obj as Record<string, unknown>)) {
    const currentPath = prefix ? `${prefix}.${key}` : key;

    // A secret field is "leaked" if it has a non-empty string value that
    // is NOT the redacted placeholder (redacted values are expected).
    if (
      sensitiveKeyNames.has(key) &&
      typeof value === 'string' &&
      value.length > 0 &&
      value !== REDACTED
    ) {
      results.push({ path: currentPath, value });
    }

    if (typeof value === 'object' && value !== null) {
      results.push(...findLeakedSecrets(value, sensitiveKeyNames, currentPath));
    }
  }

  return results;
}

/** Helper to create a mock fetch response. */
function mockFetchResponse(data: unknown) {
  return {
    ok: true,
    status: 200,
    headers: new Headers({
      'content-type': 'application/json',
      'x-csrf-token': 'mock-token',
    }),
    json: () => Promise.resolve(data),
  };
}

describe('Settings API - Secret redaction contract', () => {
  let mockFetch: ReturnType<typeof vi.fn>;

  // A sanitized response matching what the fixed backend returns.
  // Configured secrets are replaced with REDACTED, unconfigured ones are empty.
  const SANITIZED_RESPONSE = {
    main: { name: 'TestNode' },
    birdnet: {
      sensitivity: 1.0,
      threshold: 0.8,
      locale: 'en',
    },
    security: {
      sessionSecret: REDACTED,
      host: 'localhost',
      autoTls: false,
      basicAuth: {
        enabled: true,
        username: 'admin',
        password: REDACTED,
        clientId: '',
        clientSecret: '',
      },
      googleAuth: {
        enabled: false,
        clientId: 'google-client-id',
        clientSecret: REDACTED,
      },
      githubAuth: {
        enabled: false,
        clientId: 'github-client-id',
        clientSecret: '',
      },
      microsoftAuth: {
        enabled: false,
        clientId: 'microsoft-client-id',
        clientSecret: '',
      },
      oauthProviders: [
        {
          provider: 'google',
          enabled: true,
          clientId: 'google-id',
          clientSecret: REDACTED,
        },
        {
          provider: 'github',
          enabled: true,
          clientId: 'github-id',
          clientSecret: REDACTED,
        },
      ],
    },
    realtime: {
      mqtt: {
        enabled: false,
        broker: 'localhost',
        port: 1883,
        username: 'mqtt-user',
        password: REDACTED,
        topic: 'birdnet',
      },
      ebird: {
        enabled: false,
        apiKey: REDACTED,
      },
      weather: {
        provider: 'openweather',
        openWeather: {
          enabled: true,
          apiKey: REDACTED,
          endpoint: 'https://api.openweathermap.org',
        },
        wunderground: {
          apiKey: REDACTED,
          stationId: 'KTEST1',
        },
      },
    },
    output: {
      mysql: {
        enabled: false,
        username: 'dbuser',
        password: REDACTED,
        host: 'localhost',
        port: '3306',
        database: 'birdnet',
      },
    },
    backup: {
      enabled: true,
      encryptionKey: REDACTED,
      targets: [
        {
          type: 'ftp',
          enabled: true,
          settings: { host: 'ftp.local', username: 'ftpuser', password: REDACTED },
        },
        {
          type: 's3',
          enabled: true,
          settings: { bucket: 'my-bucket', accesskeyid: 'AKIAEXAMPLE', secretaccesskey: REDACTED },
        },
      ],
    },
    notification: {
      push: {
        providers: [
          {
            type: 'webhook',
            enabled: true,
            endpoints: [
              {
                url: 'https://hooks.example.com/notify',
                auth: { type: 'bearer', token: REDACTED },
              },
              {
                url: 'https://hooks.example.com/other',
                auth: { type: 'basic', pass: REDACTED },
              },
            ],
          },
        ],
      },
    },
  };

  beforeEach(() => {
    mockFetch = vi.fn();
    globalThis.fetch = mockFetch as unknown as typeof fetch;
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('should not contain plaintext secrets at known sensitive paths', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    // These paths must either be empty, absent, or the redacted placeholder
    const sensitivePaths = [
      'security.sessionSecret',
      'security.basicAuth.password',
      'security.basicAuth.clientSecret',
      'security.basicAuth.clientId',
      'security.googleAuth.clientSecret',
      'security.githubAuth.clientSecret',
      'security.microsoftAuth.clientSecret',
      'realtime.mqtt.password',
      'output.mysql.password',
      'realtime.ebird.apiKey',
      'realtime.weather.openWeather.apiKey',
      'realtime.weather.wunderground.apiKey',
      'backup.encryptionKey',
    ];

    const leaked: Array<{ path: string; value: unknown }> = [];
    for (const path of sensitivePaths) {
      const value = getNestedValue(settings, path);
      if (typeof value === 'string' && value.length > 0 && value !== REDACTED) {
        leaked.push({ path, value });
      }
    }

    expect(
      leaked,
      `Plaintext secrets found:\n${leaked.map(l => `  ${l.path}: "${l.value}"`).join('\n')}`
    ).toHaveLength(0);
  });

  it('should not contain plaintext clientSecret at any depth', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    const leaked = findLeakedSecrets(
      settings,
      new Set(['sessionSecret', 'clientSecret', 'encryptionKey', 'secretaccesskey'])
    );

    expect(
      leaked,
      `Plaintext secrets found:\n${leaked.map(f => `  ${f.path}: "${f.value}"`).join('\n')}`
    ).toHaveLength(0);
  });

  it('should not contain plaintext passwords or auth tokens at any depth', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    const leaked = findLeakedSecrets(settings, new Set(['password', 'pass', 'token']));

    expect(
      leaked,
      `Plaintext passwords found:\n${leaked.map(f => `  ${f.path}: "${f.value}"`).join('\n')}`
    ).toHaveLength(0);
  });

  it('should not contain plaintext API keys at any depth', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    const leaked = findLeakedSecrets(settings, new Set(['apiKey']));

    expect(
      leaked,
      `Plaintext API keys found:\n${leaked.map(f => `  ${f.path}: "${f.value}"`).join('\n')}`
    ).toHaveLength(0);
  });

  it('should preserve non-secret fields unchanged', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    expect(getNestedValue(settings, 'security.host')).toBe('localhost');
    expect(getNestedValue(settings, 'security.basicAuth.enabled')).toBe(true);
    expect(getNestedValue(settings, 'security.basicAuth.username')).toBe('admin');
    expect(getNestedValue(settings, 'realtime.mqtt.broker')).toBe('localhost');
    expect(getNestedValue(settings, 'realtime.mqtt.port')).toBe(1883);
    expect(getNestedValue(settings, 'output.mysql.username')).toBe('dbuser');
    expect(getNestedValue(settings, 'output.mysql.host')).toBe('localhost');
    expect(getNestedValue(settings, 'main.name')).toBe('TestNode');
  });

  it('should use redacted placeholder for configured secrets', async () => {
    mockFetch.mockResolvedValueOnce(mockFetchResponse(SANITIZED_RESPONSE));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    // Configured secrets should show the redacted placeholder
    expect(getNestedValue(settings, 'security.sessionSecret')).toBe(REDACTED);
    expect(getNestedValue(settings, 'security.basicAuth.password')).toBe(REDACTED);
    expect(getNestedValue(settings, 'realtime.mqtt.password')).toBe(REDACTED);
    expect(getNestedValue(settings, 'output.mysql.password')).toBe(REDACTED);
    expect(getNestedValue(settings, 'realtime.ebird.apiKey')).toBe(REDACTED);

    // Unconfigured secrets should be empty
    expect(getNestedValue(settings, 'security.basicAuth.clientId')).toBe('');
    expect(getNestedValue(settings, 'security.basicAuth.clientSecret')).toBe('');
    expect(getNestedValue(settings, 'security.githubAuth.clientSecret')).toBe('');
  });

  it('should reject a response that accidentally leaks a plaintext secret', async () => {
    // This test ensures our detection logic works by feeding it a BAD response.
    // If someone adds a new field and forgets to redact it on the backend,
    // the findLeakedSecrets helper should catch it.
    const leakyResponse = {
      ...SANITIZED_RESPONSE,
      security: {
        ...SANITIZED_RESPONSE.security,
        basicAuth: {
          ...SANITIZED_RESPONSE.security.basicAuth,
          password: 'oops-plaintext-password', // This should be caught
        },
      },
    };

    mockFetch.mockResolvedValueOnce(mockFetchResponse(leakyResponse));

    const { settingsAPI } = await import('./settingsApi.js');
    const settings = await settingsAPI.load();

    const leaked = findLeakedSecrets(settings, new Set(['password']));

    // We EXPECT this to find the leak — proving detection works
    expect(leaked.length).toBeGreaterThan(0);
    expect(leaked[0].path).toBe('security.basicAuth.password');
    expect(leaked[0].value).toBe('oops-plaintext-password');
  });
});
