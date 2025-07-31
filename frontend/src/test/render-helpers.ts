/**
 * Typed test rendering utilities for Svelte 5 components
 *
 * Provides type-safe alternatives to `render(Component as any)` pattern
 * while maintaining developer experience and compile-time safety.
 */

import { render, type RenderOptions, type RenderResult } from '@testing-library/svelte';
import type { ComponentProps, SvelteComponent } from 'svelte';

/**
 * Type-safe render function for Svelte components
 * Maintains prop type checking while handling internal casting
 */
export function renderTyped<T extends SvelteComponent>(
  Component: new (...args: unknown[]) => T,
  options: {
    props?: ComponentProps<T>;
  } & Omit<RenderOptions, 'props'> = {}
): RenderResult {
  // Handle the casting internally while maintaining type safety for consumers
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(Component as any, options);
}

/**
 * Create a test factory for a specific component with default props
 * Useful for components tested multiple times with similar setups
 */
export function createComponentTestFactory<T extends SvelteComponent>(
  Component: new (...args: unknown[]) => T,
  defaultProps: Partial<ComponentProps<T>> = {}
) {
  return {
    render: (
      props: Partial<ComponentProps<T>> = {},
      options: Omit<RenderOptions, 'props'> = {}
    ) => {
      return renderTyped(Component, {
        props: { ...defaultProps, ...props } as ComponentProps<T>,
        ...options,
      });
    },

    // Helper for testing with different prop combinations
    renderWithProps: (...propVariants: Partial<ComponentProps<T>>[]) => {
      return propVariants.map(props =>
        renderTyped(Component, {
          props: { ...defaultProps, ...props } as ComponentProps<T>,
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    mocked[key as keyof T] = (globalThis as any).vi.fn(handler);
  }
  return mocked;
}

/**
 * Helper for components with required props - ensures all required props are provided
 */
export function renderWithRequiredProps<T extends SvelteComponent>(
  Component: new (...args: unknown[]) => T,
  requiredProps: ComponentProps<T>,
  additionalProps: Partial<ComponentProps<T>> = {},
  options: Omit<RenderOptions, 'props'> = {}
): RenderResult {
  return renderTyped(Component, {
    props: { ...requiredProps, ...additionalProps },
    ...options,
  });
}

// Re-export testing library utilities for convenience
export { screen, fireEvent, waitFor, act } from '@testing-library/svelte';
