import { render } from '@testing-library/svelte';
import { vi } from 'vitest';
import type { ComponentType, SvelteComponent } from 'svelte';

/**
 * Component render options
 */
interface RenderOptions {
  props?: Record<string, unknown>;
  [key: string]: unknown;
}

/**
 * Custom render function that includes common test setup
 */
export function renderComponent<T extends SvelteComponent>(
  Component: ComponentType<T>,
  props: Record<string, unknown> = {},
  options: RenderOptions = {}
) {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(Component as any, {
    ...options,
    props: {
      ...props,
      ...options.props,
    },
  });
}

/**
 * Mock fetch responses for testing
 */
export function mockFetch(url: string, response: unknown, status: number = 200): void {
  globalThis.fetch = vi.fn().mockImplementation((requestUrl: string) => {
    if (requestUrl.includes(url)) {
      return Promise.resolve({
        ok: status >= 200 && status < 300,
        status,
        json: () => Promise.resolve(response),
        text: () => Promise.resolve(JSON.stringify(response)),
        headers: new Headers(),
        statusText: status >= 200 && status < 300 ? 'OK' : 'Error',
        type: 'basic' as ResponseType,
        url: requestUrl,
        redirected: false,
        body: null,
        bodyUsed: false,
        arrayBuffer: () => Promise.resolve(new ArrayBuffer(0)),
        blob: () => Promise.resolve(new Blob()),
        formData: () => Promise.resolve(new FormData()),
        clone: function () {
          return this;
        },
      } as Response);
    }
    return Promise.reject(new Error('Not found'));
  });
}

/**
 * Wait for async updates
 */
export function waitFor(ms: number = 0): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

/**
 * Create a mock CSRF token
 */
export function mockCsrfToken(): void {
  document.cookie = 'csrf_=mock-csrf-token; path=/';
}

/**
 * Clean up after tests
 */
export function cleanup(): void {
  document.cookie = 'csrf_=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;';
  vi.clearAllMocks();
}
