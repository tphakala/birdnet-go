// See https://kit.svelte.dev/docs/types#app
// for information about these interfaces

export interface BirdnetConfig {
  csrfToken?: string;
  security?: {
    enabled: boolean;
    accessAllowed: boolean;
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
