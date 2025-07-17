import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import Badge from './Badge.svelte';
import BadgeTestWrapper from './Badge.test.svelte';

describe('Badge', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: { text: 'Default Badge' },
    });

    const badge = screen.getByText('Default Badge');
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveClass('badge');
    expect(badge.tagName).toBe('SPAN');
  });

  it('renders with different variants', () => {
    const variants = [
      'primary',
      'secondary',
      'accent',
      'neutral',
      'info',
      'success',
      'warning',
      'error',
      'ghost',
    ] as const;

    variants.forEach(variant => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { unmount } = render(Badge as any, {
        props: { text: `${variant} badge`, variant },
      });

      const badge = screen.getByText(`${variant} badge`);
      if (variant === 'neutral') {
        expect(badge).toHaveClass('badge');
      } else {
        expect(badge).toHaveClass(`badge-${variant}`);
      }

      unmount();
    });
  });

  it('renders with different sizes', () => {
    const sizes = ['xs', 'sm', 'md', 'lg'] as const;

    sizes.forEach(size => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { unmount } = render(Badge as any, {
        props: { text: `${size} size`, size },
      });

      const badge = screen.getByText(`${size} size`);
      if (size === 'md') {
        // md size has no additional class
        expect(badge).toHaveClass('badge');
        expect(badge).not.toHaveClass('badge-md');
      } else {
        expect(badge).toHaveClass(`badge-${size}`);
      }

      unmount();
    });
  });

  it('renders with outline style', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: {
        text: 'Outlined',
        variant: 'primary',
        outline: true,
      },
    });

    const badge = screen.getByText('Outlined');
    expect(badge).toHaveClass('badge-primary', 'badge-outline');
  });

  it('renders with custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: {
        text: 'Custom',
        className: 'custom-badge-class',
      },
    });

    const badge = screen.getByText('Custom');
    expect(badge).toHaveClass('custom-badge-class');
  });

  it('renders with children snippet', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(BadgeTestWrapper as any);

    const icon = screen.getByTestId('badge-icon');
    expect(icon).toBeInTheDocument();
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('spreads additional props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: {
        text: 'Badge with ID',
        id: 'test-badge',
        'data-testid': 'badge-element',
      },
    });

    const badge = screen.getByTestId('badge-element');
    expect(badge).toHaveAttribute('id', 'test-badge');
  });

  it('combines multiple classes correctly', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: {
        text: 'Combined',
        variant: 'error',
        size: 'lg',
        outline: true,
        className: 'ml-2',
      },
    });

    const badge = screen.getByText('Combined');
    expect(badge).toHaveClass('badge', 'badge-error', 'badge-outline', 'badge-lg', 'ml-2');
  });

  it('handles ghost variant correctly', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Badge as any, {
      props: {
        text: 'Ghost',
        variant: 'ghost',
      },
    });

    const badge = screen.getByText('Ghost');
    expect(badge).toHaveClass('badge-ghost');
    expect(badge).not.toHaveClass('badge-outline');
  });
});
