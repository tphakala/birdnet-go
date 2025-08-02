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

// Re-export testing library utilities for convenience
export { screen, fireEvent, waitFor, act } from '@testing-library/svelte';
