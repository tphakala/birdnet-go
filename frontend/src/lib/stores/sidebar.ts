/**
 * Sidebar state store with localStorage persistence
 *
 * Manages the collapsed/expanded state of the desktop sidebar.
 * Only applies to desktop viewports (>=1024px) - mobile uses drawer overlay.
 */
import { writable, get } from 'svelte/store';
import { getStoredValue, setStoredValue } from '$lib/utils/storage';

const STORAGE_KEY = 'sidebar-collapsed';

function createSidebarStore() {
  const store = writable<boolean>(getStoredValue(STORAGE_KEY, false));
  const { subscribe, set, update } = store;

  // Auto-persist to localStorage whenever the store value changes
  if (typeof window !== 'undefined') {
    subscribe(value => setStoredValue(STORAGE_KEY, value));
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
