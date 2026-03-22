import { describe, it, expect, vi, beforeEach } from 'vitest';

// Mock @sentry/browser before importing the module under test
vi.mock('@sentry/browser', () => ({
  init: vi.fn(),
  captureException: vi.fn(),
  setTag: vi.fn(),
  withScope: vi.fn((callback: (scope: unknown) => void) => {
    const mockScope = {
      setLevel: vi.fn(),
      setTag: vi.fn(),
      setContext: vi.fn(),
    };
    callback(mockScope);
  }),
}));

import * as Sentry from '@sentry/browser';
import { initSentry, captureApiError, captureError } from './sentry';

describe('initSentry', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('calls Sentry.init with correct config', () => {
    initSentry({ dsn: 'https://test@sentry.io/123', systemId: 'sys-1', version: '1.0.0' });

    expect(Sentry.init).toHaveBeenCalledOnce();
    const config = vi.mocked(Sentry.init).mock.calls[0][0];
    expect(config).toMatchObject({
      dsn: 'https://test@sentry.io/123',
      release: 'birdnet-go@1.0.0',
      environment: 'production',
      sampleRate: 1.0,
      tracesSampleRate: 0,
      maxBreadcrumbs: 20,
    });
    expect(config?.beforeSend).toBeTypeOf('function');
    expect(config?.initialScope).toMatchObject({
      tags: { systemId: 'sys-1', source: 'frontend' },
    });
  });
});

describe('beforeSend privacy filtering', () => {
  type BeforeSendFn = NonNullable<Parameters<typeof Sentry.init>[0]>['beforeSend'];
  let beforeSend: BeforeSendFn;

  beforeEach(() => {
    vi.clearAllMocks();
    initSentry({ dsn: 'https://test@sentry.io/123', systemId: 'sys-1', version: '1.0.0' });
    beforeSend = vi.mocked(Sentry.init).mock.calls[0][0]?.beforeSend as BeforeSendFn;
  });

  it('registers a beforeSend hook', () => {
    expect(beforeSend).toBeDefined();
    expect(beforeSend).toBeTypeOf('function');
  });

  it('strips user data', () => {
    const event = { type: undefined, user: { ip_address: '1.2.3.4' } } as Sentry.ErrorEvent;
    const result = beforeSend?.(event, {} as Sentry.EventHint);
    expect(result).not.toBeNull();
    expect((result as Sentry.ErrorEvent).user).toBeUndefined();
  });

  it('strips server_name', () => {
    const event = { type: undefined, server_name: 'my-host' } as Sentry.ErrorEvent;
    const result = beforeSend?.(event, {} as Sentry.EventHint);
    expect((result as Sentry.ErrorEvent).server_name).toBeUndefined();
  });

  it('scrubs main event request URL query params', () => {
    const event = {
      type: undefined,
      request: { url: 'https://birdnet.local/settings?apiKey=secret&token=abc' },
    } as Sentry.ErrorEvent;
    const result = beforeSend?.(event, {} as Sentry.EventHint) as Sentry.ErrorEvent;
    expect(result.request?.url).toBe('/settings');
  });

  it('scrubs breadcrumb fetch URLs to path only', () => {
    const event = {
      type: undefined,
      breadcrumbs: [
        {
          category: 'fetch',
          data: { url: 'https://birdnet.local/api/v2/settings?id=secret' },
        },
        {
          category: 'ui.click',
          data: { target: 'button.save' },
        },
      ],
    } as Sentry.ErrorEvent;
    const result = beforeSend?.(event, {} as Sentry.EventHint) as Sentry.ErrorEvent;
    expect(result.breadcrumbs?.[0]?.data?.url).toBe('/api/v2/settings');
    expect(result.breadcrumbs?.[1]?.data?.target).toBe('button.save');
  });

  it('strips request/response bodies from breadcrumbs', () => {
    const event = {
      type: undefined,
      breadcrumbs: [
        {
          category: 'fetch',
          data: {
            url: '/api/test',
            request_body: '{"password":"secret"}',
            response_body: '{"token":"abc"}',
            body: 'raw body',
          },
        },
      ],
    } as Sentry.ErrorEvent;
    const result = beforeSend?.(event, {} as Sentry.EventHint) as Sentry.ErrorEvent;
    expect(result.breadcrumbs?.[0]?.data?.request_body).toBeUndefined();
    expect(result.breadcrumbs?.[0]?.data?.response_body).toBeUndefined();
    expect(result.breadcrumbs?.[0]?.data?.body).toBeUndefined();
  });
});

describe('captureApiError', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    initSentry({ dsn: 'https://test@sentry.io/123', systemId: 'sys-1', version: '1.0.0' });
  });

  it('skips 401 errors', () => {
    const error = new Error('Unauthorized') as Error & { status: number; isNetworkError: boolean };
    error.status = 401;
    error.isNetworkError = false;
    captureApiError(error);
    expect(Sentry.captureException).not.toHaveBeenCalled();
  });

  it('skips 403 errors', () => {
    const error = new Error('Forbidden') as Error & { status: number; isNetworkError: boolean };
    error.status = 403;
    error.isNetworkError = false;
    captureApiError(error);
    expect(Sentry.captureException).not.toHaveBeenCalled();
  });

  it('captures 500 errors with error severity', () => {
    const error = new Error('Server Error') as Error & { status: number; isNetworkError: boolean };
    error.status = 500;
    error.isNetworkError = false;
    captureApiError(error, { endpoint: '/api/v2/test', method: 'GET' });
    expect(Sentry.withScope).toHaveBeenCalledOnce();
  });

  it('captures network errors with error severity', () => {
    const error = new Error('Network Error') as Error & { status: number; isNetworkError: boolean };
    error.status = 0;
    error.isNetworkError = true;
    captureApiError(error);
    expect(Sentry.withScope).toHaveBeenCalledOnce();
  });
});

describe('captureError', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    initSentry({ dsn: 'https://test@sentry.io/123', systemId: 'sys-1', version: '1.0.0' });
  });

  it('captures Error with logger tag', () => {
    const error = new Error('test error');
    captureError(error, { category: 'ui' });

    expect(Sentry.withScope).toHaveBeenCalledOnce();
    expect(Sentry.captureException).toHaveBeenCalledWith(error);
  });

  it('sets logger.category tag from context', () => {
    const error = new Error('test error');

    const mockScope = {
      setLevel: vi.fn(),
      setTag: vi.fn(),
      setContext: vi.fn(),
    };
    vi.mocked(Sentry.withScope).mockImplementation(((callback: (scope: unknown) => void) => {
      callback(mockScope);
    }) as typeof Sentry.withScope);

    captureError(error, { category: 'settings' });

    expect(mockScope.setTag).toHaveBeenCalledWith('error.type', 'logger');
    expect(mockScope.setTag).toHaveBeenCalledWith('logger.category', 'settings');
  });

  it('works without context', () => {
    const error = new Error('bare error');

    expect(() => captureError(error)).not.toThrow();
    expect(Sentry.captureException).toHaveBeenCalledWith(error);
  });
});
