/**
 * Typed test rendering utilities for Svelte 5 components
 *
 * Provides type-safe alternatives to `render(Component as any)` pattern
 * while maintaining developer experience and compile-time safety.
 */

import { render, type RenderOptions, type RenderResult } from '@testing-library/svelte';
import type { ComponentProps, Component } from 'svelte';

/**
 * Type-safe render function for Svelte 5 components
 * Maintains prop type checking while handling internal casting
 */
export function renderTyped<TComponent extends Component<Record<string, unknown>>>(
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
export function createComponentTestFactory<TComponent extends Component<Record<string, unknown>>>(
  Component: TComponent,
  defaultProps: Partial<ComponentProps<TComponent>> = {}
) {
  return {
    render: (
      propsOrOptions: unknown = {},
      options: Omit<RenderOptions, 'props'> = {}
    ): RenderResult<ComponentProps<TComponent>> => {
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
        props = (propsOrOptions as Partial<ComponentProps<TComponent>>) ?? {};
        renderOptions = options;
      }
      
      return renderTyped(Component, {
        props: { ...defaultProps, ...props } as ComponentProps<TComponent>,
        ...renderOptions,
      });
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    mocked[key as keyof T] = (globalThis as any).vi.fn(handler);
  }
  return mocked;
}

/**
 * Helper for components with required props - ensures all required props are provided
 */
export function renderWithRequiredProps<TComponent extends Component<Record<string, unknown>>>(
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

// Re-export testing library utilities for convenience
export { screen, fireEvent, waitFor, act } from '@testing-library/svelte';
