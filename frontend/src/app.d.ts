// See https://kit.svelte.dev/docs/types#app
// for information about these interfaces

/**
 * OAuth endpoint configuration for custom auth providers.
 */
export interface AuthEndpoints {
  google?: string;
  github?: string;
  microsoft?: string;
}

/**
 * Authentication provider configuration.
 * Shared type used by App.svelte, RootLayout, DesktopSidebar, and LoginModal.
 */
export interface AuthConfig {
  basicEnabled: boolean;
  googleEnabled: boolean;
  githubEnabled: boolean;
  microsoftEnabled: boolean;
  endpoints?: AuthEndpoints;
}

export interface BirdnetConfig {
  csrfToken?: string;
  security?: {
    enabled: boolean;
    accessAllowed: boolean;
    authConfig?: AuthConfig;
  };
  version?: string;
  currentPath?: string;
}

declare global {
  namespace App {
    // interface Error {}
    // interface Locals {}
    // interface PageData {}
    // interface PageState {}
    // interface Platform {}
  }

  interface Window {
    BIRDNET_CONFIG?: BirdnetConfig;
  }
}

export {};
