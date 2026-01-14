import { writable, derived } from 'svelte/store';

export type ToastType = 'info' | 'success' | 'warning' | 'error';
export type ToastPosition =
  | 'top-left'
  | 'top-center'
  | 'top-right'
  | 'bottom-left'
  | 'bottom-center'
  | 'bottom-right';

export interface ToastMessage {
  id: string;
  type: ToastType;
  message: string;
  duration?: number | null; // null means no auto-dismiss
  position?: ToastPosition;
  showIcon?: boolean;
  actions?: Array<{ label: string; onClick: () => void }>;
}

interface ToastState {
  toasts: ToastMessage[];
  defaultPosition: ToastPosition;
  defaultDuration: number;
}

// Maximum number of toasts to display at once
const MAX_TOASTS = 3;

const initialState: ToastState = {
  toasts: [],
  defaultPosition: 'top-right',
  defaultDuration: 5000,
};

// Create the writable store
const toastStore = writable<ToastState>(initialState);

// Export derived store for component access
export const toasts = derived(toastStore, $store => $store.toasts);
export const $toastStore = toastStore;

// Helper function to generate unique IDs
function generateId(): string {
  return `toast-${Date.now()}-${Math.random().toString(36).substring(2, 11)}`;
}

// Toast management functions
export const toastActions = {
  /**
   * Show a toast notification
   */
  show(
    message: string,
    type: ToastType = 'info',
    options: Partial<Omit<ToastMessage, 'id' | 'type' | 'message'>> = {}
  ): string {
    const id = generateId();
    const state = get(toastStore);

    const toast: ToastMessage = {
      id,
      type,
      message,
      duration: options.duration ?? state.defaultDuration,
      position: options.position ?? state.defaultPosition,
      showIcon: options.showIcon ?? true,
      actions: options.actions ?? [],
    };

    toastStore.update(state => {
      let newToasts = [...state.toasts, toast];
      // Remove oldest toasts if we exceed the limit
      if (newToasts.length > MAX_TOASTS) {
        newToasts = newToasts.slice(-MAX_TOASTS);
      }
      return {
        ...state,
        toasts: newToasts,
      };
    });

    return id;
  },

  /**
   * Show an info toast
   */
  info(message: string, options?: Partial<Omit<ToastMessage, 'id' | 'type' | 'message'>>): string {
    return this.show(message, 'info', options);
  },

  /**
   * Show a success toast
   */
  success(
    message: string,
    options?: Partial<Omit<ToastMessage, 'id' | 'type' | 'message'>>
  ): string {
    return this.show(message, 'success', options);
  },

  /**
   * Show a warning toast
   */
  warning(
    message: string,
    options?: Partial<Omit<ToastMessage, 'id' | 'type' | 'message'>>
  ): string {
    return this.show(message, 'warning', options);
  },

  /**
   * Show an error toast
   */
  error(message: string, options?: Partial<Omit<ToastMessage, 'id' | 'type' | 'message'>>): string {
    return this.show(message, 'error', options);
  },

  /**
   * Remove a toast by ID
   */
  remove(id: string): void {
    toastStore.update(state => ({
      ...state,
      toasts: state.toasts.filter(toast => toast.id !== id),
    }));
  },

  /**
   * Clear all toasts
   */
  clear(): void {
    toastStore.update(state => ({
      ...state,
      toasts: [],
    }));
  },

  /**
   * Update default position
   */
  setDefaultPosition(position: ToastPosition): void {
    toastStore.update(state => ({
      ...state,
      defaultPosition: position,
    }));
  },

  /**
   * Update default duration
   */
  setDefaultDuration(duration: number): void {
    toastStore.update(state => ({
      ...state,
      defaultDuration: duration,
    }));
  },
};

// Svelte doesn't have 'get' in the global scope, so we need to import it
import { get } from 'svelte/store';
