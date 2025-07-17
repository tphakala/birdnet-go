// See https://kit.svelte.dev/docs/types#app
// for information about these interfaces
declare global {
  namespace App {
    // interface Error {}
    // interface Locals {}
    // interface PageData {}
    // interface PageState {}
    // interface Platform {}
  }

  interface Window {
    BIRDNET_CONFIG?: {
      csrfToken: string;
      security: {
        enabled: boolean;
        accessAllowed: boolean;
      };
      version: string;
      currentPath: string;
    };
  }
}

export {};
