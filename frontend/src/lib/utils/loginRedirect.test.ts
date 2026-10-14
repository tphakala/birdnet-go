import { describe, it, expect, afterEach } from 'vitest';
import {
  resolveLoginRedirect,
  buildLoginUrl,
  DEFAULT_LOGIN_TARGET,
  LOGIN_ROUTE_PATH,
} from './loginRedirect';
import { setBasePath, resetBasePath } from './urlHelpers';

function search(value: string): string {
  return `?${new URLSearchParams({ redirect: value }).toString()}`;
}

describe('resolveLoginRedirect', () => {
  afterEach(() => {
    resetBasePath();
  });

  it.each([
    ['a settings page', '/ui/settings/security', '/ui/settings/security'],
    [
      'the query is kept',
      '/ui/detections?species=Robin&offset=0',
      '/ui/detections?species=Robin&offset=0',
    ],
    ['the hash is dropped', '/ui/settings/audio?tab=eq#frag', '/ui/settings/audio?tab=eq'],
    ['a literal dot segment is resolved', '/ui/a/../settings', '/ui/settings'],
  ])('keeps a valid target: %s', (_name, value, expected) => {
    expect(resolveLoginRedirect(search(value))).toBe(expected);
  });

  it('returns the default when the parameter is missing or empty', () => {
    expect(resolveLoginRedirect('')).toBe(DEFAULT_LOGIN_TARGET);
    expect(resolveLoginRedirect('?other=1')).toBe(DEFAULT_LOGIN_TARGET);
    expect(resolveLoginRedirect('?redirect=')).toBe(DEFAULT_LOGIN_TARGET);
  });

  it.each([
    '/ui',
    '/ui/',
    'https://evil.example/ui/x',
    '//evil.example/ui/x',
    '/\\evil.example',
    '\\\\evil',
    'javascript:alert(1)',
    '/javascript:alert(1)',
    '/ui/../api/v2/settings',
    '/ui/%2e%2e/api',
    '/ui/%252e%252e/api',
    '/ui/..%2fapi',
    '/ui///evil.com',
    '/ui/%5cfoo',
    '/uix/a',
    '/api/v2/x',
    '/',
    '/login?redirect=/ui/x',
    '/ui/login',
    '/ui/login/',
    '/ui/login//',
    '/ui//login',
    '/ui/%6cogin',
    '/ui/x%00y',
    '/ui/Login',
    `/ui/${'a'.repeat(520)}`,
    `/ui/x?q=${'a'.repeat(2000)}`,
  ])('returns the default for %s', value => {
    expect(resolveLoginRedirect(search(value))).toBe(DEFAULT_LOGIN_TARGET);
  });

  describe('behind a reverse proxy prefix', () => {
    it('strips the prefix from a prefixed target', () => {
      setBasePath('/proxy');
      expect(resolveLoginRedirect(search('/proxy/ui/settings'))).toBe('/ui/settings');
    });

    it('keeps an unprefixed target', () => {
      setBasePath('/proxy');
      expect(resolveLoginRedirect(search('/ui/settings'))).toBe('/ui/settings');
    });

    it('does not strip a prefix that is not a whole segment', () => {
      setBasePath('/proxy');
      expect(resolveLoginRedirect(search('/proxyui/settings'))).toBe(DEFAULT_LOGIN_TARGET);
    });

    it('rejects a prefixed login route', () => {
      setBasePath('/proxy');
      expect(resolveLoginRedirect(search('/proxy/ui/login'))).toBe(DEFAULT_LOGIN_TARGET);
    });
  });
});

describe('buildLoginUrl', () => {
  afterEach(() => {
    resetBasePath();
  });

  it('round-trips through resolveLoginRedirect', () => {
    const target = '/ui/detections?species=Robin&offset=0';
    const url = buildLoginUrl(target);

    expect(url.startsWith(`${LOGIN_ROUTE_PATH}?redirect=`)).toBe(true);
    expect(resolveLoginRedirect(url.slice(url.indexOf('?')))).toBe(target);
  });

  it('carries the proxy prefix', () => {
    setBasePath('/proxy');

    expect(buildLoginUrl('/ui/settings').startsWith('/proxy/ui/login?redirect=')).toBe(true);
  });
});
