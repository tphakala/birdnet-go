/**
 * Typed test rendering utilities for Svelte 5 components
 *
 * Provides type-safe alternatives to `render(Component as any)` pattern
 * while maintaining developer experience and compile-time safety.
 */

import { render, type RenderOptions, type RenderResult } from '@testing-library/svelte';
import type { ComponentProps, Component } from 'svelte';
import { vi } from 'vitest';

/**
 * Type-safe render function for Svelte 5 components
 * Maintains prop type checking while handling internal casting
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function renderTyped<TComponent extends Component<any>>(
  Component: TComponent,
  options: {
    props?: ComponentProps<TComponent>;
  } & Omit<RenderOptions, 'props'> = {}
): RenderResult<ComponentProps<TComponent>> {
  // Handle the casting internally while maintaining type safety for consumers
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(Component as any, options);
}

/**
 * Create a test factory for a specific component with default props
 * Useful for components tested multiple times with similar setups
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function createComponentTestFactory<TComponent extends Component<any>>(
  Component: TComponent,
  defaultProps: Partial<ComponentProps<TComponent>> = {}
) {
  return {
    render: (
      propsOrOptions: unknown = {},
      options: Omit<RenderOptions, 'props'> = {}
    ): RenderResult<ComponentProps<TComponent>> & {
      rerender: (newProps: Partial<ComponentProps<TComponent>>) => Promise<void>;
    } => {
      // Handle both patterns: direct props or {props: {...}} wrapper
      let props: Partial<ComponentProps<TComponent>>;
      let renderOptions: Omit<RenderOptions, 'props'>;

      if (propsOrOptions && typeof propsOrOptions === 'object' && 'props' in propsOrOptions) {
        // Handle {props: {...}, ...otherOptions} pattern
        const { props: extractedProps, ...otherOptions } = propsOrOptions;
        props = extractedProps ?? {};
        renderOptions = { ...otherOptions, ...options };
      } else {
        // Handle direct props pattern
        props = propsOrOptions ? (propsOrOptions as Partial<ComponentProps<TComponent>>) : {};
        renderOptions = options;
      }

      const result = renderTyped(Component, {
        props: { ...defaultProps, ...props } as ComponentProps<TComponent>,
        ...renderOptions,
      });

      // Add typed rerender function
      return {
        ...result,
        rerender: async (newProps: Partial<ComponentProps<TComponent>>) => {
          // Use the original rerender with properly typed props (new API - direct props)
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          await (result as any).rerender({
            ...defaultProps,
            ...newProps,
          } as ComponentProps<TComponent>);
        },
      };
    },

    // Helper for testing with different prop combinations
    renderWithProps: (...propVariants: Partial<ComponentProps<TComponent>>[]) => {
      return propVariants.map(props =>
        renderTyped(Component, {
          props: { ...defaultProps, ...props } as ComponentProps<TComponent>,
        })
      );
    },
  };
}

/**
 * Utility for components that require event handlers in tests
 */
export function createMockHandlers<T extends Record<string, (...args: unknown[]) => unknown>>(
  handlers: T
): T {
  const mocked = {} as T;
  for (const [key, handler] of Object.entries(handlers)) {
    mocked[key as keyof T] = vi.fn(handler) as unknown as T[keyof T];
  }
  return mocked;
}

/**
 * Helper for components with required props - ensures all required props are provided
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function renderWithRequiredProps<TComponent extends Component<any>>(
  Component: TComponent,
  requiredProps: ComponentProps<TComponent>,
  additionalProps: Partial<ComponentProps<TComponent>> = {},
  options: Omit<RenderOptions, 'props'> = {}
): RenderResult<ComponentProps<TComponent>> {
  return renderTyped(Component, {
    props: { ...requiredProps, ...additionalProps },
    ...options,
  });
}

/**
 * Common test mock utilities
 */

/**
 * Mock i18n function with template variable support
 * @param translations - Object mapping translation keys to translated strings
 * @returns Mock function that handles translation keys and template variables
 */
export function createI18nMock(translations: Record<string, string>) {
  return vi.fn((key: string, params?: Record<string, unknown>) => {
    // eslint-disable-next-line security/detect-object-injection
    let translation = translations[key] ?? key;

    // Handle template variables like {{variable}}
    if (params && typeof translation === 'string') {
      Object.entries(params).forEach(([param, value]) => {
        translation = translation.replace(`{{${param}}}`, String(value));
      });
    }

    return translation;
  });
}

/**
 * Mock DOM APIs commonly needed in component tests
 * Sets up ResizeObserver, getComputedStyle, and focus functionality
 */
export function mockDOMAPIs() {
  // Mock ResizeObserver
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  if (!(globalThis as any).ResizeObserver) {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (globalThis as any).ResizeObserver = vi.fn().mockImplementation(() => ({
      observe: vi.fn(),
      unobserve: vi.fn(),
      disconnect: vi.fn(),
    }));
  }

  // Mock getComputedStyle for modal and accessibility tests
  if (!Object.getOwnPropertyDescriptor(window, 'getComputedStyle')) {
    Object.defineProperty(window, 'getComputedStyle', {
      value: vi.fn(() => ({
        getPropertyValue: vi.fn(() => ''),
        visibility: 'visible',
        display: 'block',
      })),
      writable: true,
    });
  }

  // Mock focus for accessibility tests
  if (!Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'focus')) {
    Object.defineProperty(HTMLElement.prototype, 'focus', {
      value: vi.fn(),
      writable: true,
    });
  }
}

/**
 * Mock logger utilities for components that use the centralized logger
 * @param categories - Array of logger categories to mock (e.g., ['audio', 'ui'])
 * @returns Object with mocked logger functions
 */
export function createLoggerMocks(categories: string[] = ['audio', 'ui', 'api']) {
  const mockLogger = {
    warn: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
    debug: vi.fn(),
  };

  const loggers: Record<string, typeof mockLogger> = {};
  categories.forEach(category => {
    // eslint-disable-next-line security/detect-object-injection
    loggers[category] = mockLogger;
  });

  return { loggers, mockLogger };
}

/**
 * Common i18n translations for components that frequently use these keys
 */
export const commonI18nTranslations = {
  // Media controls
  'media.audio.play': 'Play',
  'media.audio.pause': 'Pause',
  'media.audio.download': 'Download audio file',
  'media.audio.volume': 'Volume control',
  'media.audio.filterControl': 'Filter control',
  'media.audio.seekProgress': 'Seek audio progress',
  'media.audio.volumeGain': 'Volume gain: {{value}} dB',
  'media.audio.highPassFilter': 'High-pass filter: {{freq}} Hz',

  // Common buttons
  'common.buttons.clear': 'Clear',
  'common.buttons.save': 'Save',
  'common.buttons.cancel': 'Cancel',
  'common.buttons.delete': 'Delete',
  'common.buttons.edit': 'Edit',

  // Form labels and validation
  'forms.validation.required': 'This field is required',
  'forms.validation.email': 'Please enter a valid email address',
  'forms.validation.password': 'Password must be at least 8 characters',

  // Authentication
  'auth.login.title': 'Login',
  'auth.login.password': 'Password',
  'auth.login.submit': 'Sign In',
  'auth.login.failed': 'Login failed',
  'auth.login.success': 'Login successful',
};

// Re-export testing library utilities for convenience
export { screen, fireEvent, waitFor, act } from '@testing-library/svelte';
