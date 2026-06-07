// Project identity/routing URLs (repo, issues, discussions, releases) now come
// from the backend-resolved branding config via appState.projectLinks, so a fork
// that rebrands the backend is reflected in the UI. See internal/branding and
// AppConfigResponse.projectLinks. Only non-identity links remain hardcoded here.

// License is fixed project metadata (CC BY-NC-SA 4.0), not project identity, so
// it stays a constant rather than a configurable branding value.
export const LICENSE_URL = 'https://creativecommons.org/licenses/by-nc-sa/4.0/';
