/**
 * Navigation store for SPA client-side routing.
 * Manages route state and provides navigate() function using History API.
 */

/**
 * Navigation store interface
 */
export interface NavigationStore {
  currentPath: string;
  navigate: (url: string) => void;
  handlePopState: () => void;
}

/**
 * Normalize URL path to /ui/ format
 */
function normalizePath(url: string): string {
  // Handle root
  if (url === '/' || url === '') {
    return '/ui/dashboard';
  }
  // Already has /ui/ prefix
  if (url.startsWith('/ui/')) {
    return url;
  }
  // Add /ui/ prefix
  return `/ui${url.startsWith('/') ? url : '/' + url}`;
}

/**
 * Create a navigation store instance.
 * Used for testing and the singleton export.
 */
export function createNavigation(): NavigationStore {
  let currentPath = $state(
    typeof window !== 'undefined' ? window.location.pathname : '/ui/dashboard'
  );

  function navigate(url: string): void {
    const normalizedPath = normalizePath(url);
    currentPath = normalizedPath;

    if (typeof window !== 'undefined') {
      window.history.pushState({}, '', normalizedPath);
    }
  }

  function handlePopState(): void {
    if (typeof window !== 'undefined') {
      currentPath = window.location.pathname;
    }
  }

  return {
    get currentPath() {
      return currentPath;
    },
    navigate,
    handlePopState,
  };
}

// Singleton instance
export const navigation = createNavigation();
