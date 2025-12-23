/**
 * Sidebar state store with localStorage persistence
 *
 * Manages the collapsed/expanded state of the desktop sidebar.
 * Only applies to desktop viewports (≥1024px) - mobile uses drawer overlay.
 */
import { writable, get } from 'svelte/store';

const STORAGE_KEY = 'sidebar-collapsed';

// Check if we're in a browser environment
const isBrowser = typeof window !== 'undefined';

/**
 * Get initial collapsed state from localStorage
 * Defaults to false (expanded) if no preference is stored
 */
function getInitialState(): boolean {
  if (!isBrowser) return false;

  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored !== null) {
      return stored === 'true';
    }
  } catch {
    // localStorage not available (e.g., private browsing)
  }

  return false; // Default to expanded
}

/**
 * Save collapsed state to localStorage
 */
function saveState(collapsed: boolean): void {
  if (!isBrowser) return;

  try {
    localStorage.setItem(STORAGE_KEY, String(collapsed));
  } catch {
    // localStorage not available
  }
}

function createSidebarStore() {
  const store = writable<boolean>(getInitialState());
  const { subscribe, set, update } = store;

  // Auto-persist to localStorage whenever the store value changes
  if (isBrowser) {
    subscribe(saveState);
  }

  return {
    subscribe,

    /**
     * Get current collapsed state (for non-reactive access)
     */
    get collapsed(): boolean {
      return get(store);
    },

    /**
     * Set collapsed state (auto-persisted via subscription)
     */
    setCollapsed(value: boolean) {
      set(value);
    },

    /**
     * Toggle collapsed state
     */
    toggle() {
      update(collapsed => !collapsed);
    },

    /**
     * Expand sidebar (set collapsed to false)
     */
    expand() {
      set(false);
    },

    /**
     * Collapse sidebar (set collapsed to true)
     */
    collapse() {
      set(true);
    },
  };
}

export const sidebar = createSidebarStore();
