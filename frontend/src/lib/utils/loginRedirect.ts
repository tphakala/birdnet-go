/**
 * Login route helpers: the SPA login route and the validated post-login target.
 *
 * The backend redirects a logged-out visitor of a protected page to
 * `<base>/login?redirect=<path>`. The navigation store's generic `/ui` prefixing
 * (normalizeUiPath in navigation.svelte.ts) turns that address into the login route; the
 * SPA reads the target back with resolveLoginRedirect and hands it to the login form.
 * resolveLoginRedirect returns an internal path without a
 * reverse-proxy prefix, like the paths the navigation store keeps; buildLoginUrl returns
 * an address that carries the prefix.
 */

import { buildAppUrl, getAppBasePath, isRelativePath } from './urlHelpers';

// Path constants are built with join so nginx sub_filter cannot rewrite them in the
// bundle (see navigation.svelte.ts).

/** The SPA route that shows the login form. */
export const LOGIN_ROUTE_PATH = ['', 'ui', 'login'].join('/');
/** The path the backend redirects to; the navigation store prefixes it to the login route. */
export const LEGACY_LOGIN_PATH = ['', 'login'].join('/');
/** Where a login goes when the redirect parameter is missing or not acceptable. */
export const DEFAULT_LOGIN_TARGET = ['', 'ui', 'dashboard'].join('/');
/** Query parameter that carries the post-login target (set by the backend middleware). */
export const LOGIN_REDIRECT_PARAM = 'redirect';

/** A real page under the UI root: a bare `/ui` or `/ui/` is not a target. */
const UI_SUBPATH_RE = /^\/ui\/[^/]/;
/** Encoded separators or dots, backslashes and empty segments have no use in a UI route. */
const FORBIDDEN_RAW_PATH_RE = /\\|\/\/|%(?:2f|5c|2e)/i;
/** A dot segment left after decoding. */
const DOT_SEGMENT_RE = /\/\.\.?(?:\/|$)/;
const PERCENT_ESCAPE_RE = /%[0-9a-f]{2}/i;
const TRAILING_SLASHES_RE = /\/+$/;

/**
 * Longest redirect value accepted, query included. Intentionally a bit tighter than the
 * backend's security.MaxSafeRedirectLength (2048), so what the client sends passes server
 * validation. LoginModal applies the same cap.
 */
export const MAX_LOGIN_REDIRECT_LENGTH = 2000;
/** The backend rejects redirect paths of this length or longer (security.MaxSafePathLength). */
const MAX_LOGIN_REDIRECT_PATH_LENGTH = 512;

const SENTINEL_ORIGIN = 'http://login-redirect.internal';

const DELETE_CHAR_CODE = 0x7f;
const FIRST_PRINTABLE_CHAR_CODE = 0x20;

function hasControlChar(value: string): boolean {
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (code < FIRST_PRINTABLE_CHAR_CODE || code === DELETE_CHAR_CODE) return true;
  }
  return false;
}

/** Removes the app prefix on a segment boundary only. */
function stripAppPrefix(path: string, base: string): string {
  if (base && (path === base || path.startsWith(base + '/'))) {
    return path.slice(base.length) || '/';
  }
  return path;
}

/**
 * Reads the post-login target from a query string (`?redirect=...`).
 *
 * Returns an unprefixed internal path plus query that is a real page under `/ui/`, or
 * DEFAULT_LOGIN_TARGET when the value is missing, off-site, a login path, ambiguous
 * (encoded separators or dots, double encoding) or too long. Literal dot segments are
 * resolved, and the resolved path must still be a page under `/ui/`. It rejects more
 * than the backend does, on purpose.
 */
export function resolveLoginRedirect(search: string): string {
  const raw = new URLSearchParams(search).get(LOGIN_REDIRECT_PARAM);
  if (!raw || raw.length > MAX_LOGIN_REDIRECT_LENGTH) return DEFAULT_LOGIN_TARGET;
  if (!isRelativePath(raw)) return DEFAULT_LOGIN_TARGET;

  const rawPath = raw.split(/[?#]/, 1)[0];
  if (FORBIDDEN_RAW_PATH_RE.test(rawPath)) return DEFAULT_LOGIN_TARGET;

  let url: URL;
  let decoded: string;
  try {
    // Resolves literal dot segments and drops the hash.
    url = new URL(raw, SENTINEL_ORIGIN);
    decoded = decodeURIComponent(url.pathname);
  } catch {
    return DEFAULT_LOGIN_TARGET;
  }
  // The raw-path check above rejects '//', backslashes and encoded separators and dots, and
  // new URL resolves literal dot segments; the decoded path is checked again as defense in
  // depth.
  if (
    decoded.includes('//') ||
    decoded.includes('\\') ||
    DOT_SEGMENT_RE.test(decoded) ||
    hasControlChar(decoded) ||
    PERCENT_ESCAPE_RE.test(decoded)
  ) {
    return DEFAULT_LOGIN_TARGET;
  }

  const base = getAppBasePath();
  const path = stripAppPrefix(url.pathname, base);
  const plain = stripAppPrefix(decoded, base);
  if (!UI_SUBPATH_RE.test(path) || path.length >= MAX_LOGIN_REDIRECT_PATH_LENGTH) {
    return DEFAULT_LOGIN_TARGET;
  }

  const normalized = plain.replace(TRAILING_SLASHES_RE, '').toLowerCase();
  if (normalized === LOGIN_ROUTE_PATH || normalized === LEGACY_LOGIN_PATH) {
    return DEFAULT_LOGIN_TARGET;
  }

  return path + url.search;
}

/**
 * The address of the login route that returns to `target` after a login, with any
 * reverse-proxy prefix. `target` is an internal path plus query.
 */
export function buildLoginUrl(target: string): string {
  const query = new URLSearchParams({ [LOGIN_REDIRECT_PARAM]: target });
  return `${buildAppUrl(LOGIN_ROUTE_PATH)}?${query.toString()}`;
}
