import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import StatsCard from './StatsCard.svelte';
import StatsCardTestWrapper from './StatsCard.test.svelte';

describe('StatsCard', () => {
  it('renders with basic props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCard as any, {
      props: {
        value: '1,234',
        label: 'Total Users',
      },
    });

    expect(screen.getByText('1,234')).toBeInTheDocument();
    expect(screen.getByText('Total Users')).toBeInTheDocument();
  });

  it('renders with sub label', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCard as any, {
      props: {
        value: '99.9%',
        label: 'Uptime',
        subLabel: 'Last 30 days',
      },
    });

    expect(screen.getByText('99.9%')).toBeInTheDocument();
    expect(screen.getByText('Uptime')).toBeInTheDocument();
    expect(screen.getByText('Last 30 days')).toBeInTheDocument();
  });

  it('renders with trend data', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCard as any, {
      props: {
        value: '456',
        label: 'Active Sessions',
        trend: {
          direction: 'up',
          value: '+12%',
          label: 'vs last week',
        },
      },
    });

    expect(screen.getByText('+12% vs last week')).toBeInTheDocument();
    const svg = screen.getByText('+12% vs last week').previousElementSibling;
    expect(svg).toHaveClass('text-success');
  });

  it('renders different trend directions', () => {
    const trends = [
      { direction: 'up' as const, expectedClass: 'text-success' },
      { direction: 'down' as const, expectedClass: 'text-error' },
      { direction: 'neutral' as const, expectedClass: 'text-base-content/70' },
    ];

    trends.forEach(({ direction, expectedClass }) => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { container, unmount } = render(StatsCard as any, {
        props: {
          value: '100',
          label: 'Test',
          trend: { direction, value: '0%' },
        },
      });

      const svg = container.querySelector('svg');
      expect(svg).toHaveClass(expectedClass);
      // Verify SVG exists with correct class (lucide icons have different path data)
      expect(svg).toBeTruthy();
      unmount();
    });
  });

  it('renders with icon', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCardTestWrapper as any, {
      props: {
        showIcon: true,
      },
    });

    expect(screen.getByTestId('custom-icon')).toBeInTheDocument();
  });

  it('renders different variants', () => {
    const variants = [
      'default',
      'primary',
      'secondary',
      'accent',
      'info',
      'success',
      'warning',
      'error',
    ] as const;

    variants.forEach(variant => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const { container, unmount } = render(StatsCard as any, {
        props: {
          value: '100',
          label: 'Test',
          variant,
        },
      });

      const card = container.querySelector('.card');
      const expectedClass = variant === 'default' ? 'bg-base-100' : `bg-${variant}`;
      expect(card).toHaveClass(expectedClass);
      unmount();
    });
  });

  it('renders loading state', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '0',
        label: 'Loading',
        loading: true,
      },
    });

    const skeletons = container.querySelectorAll('.skeleton');
    expect(skeletons).toHaveLength(2);
    expect(screen.queryByText('0')).not.toBeInTheDocument();
    expect(screen.queryByText('Loading')).not.toBeInTheDocument();
  });

  it('renders loading state with sub label', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '0',
        label: 'Loading',
        subLabel: 'Sub',
        loading: true,
      },
    });

    const skeletons = container.querySelectorAll('.skeleton');
    expect(skeletons).toHaveLength(3);
  });

  it('renders as link when href provided', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '789',
        label: 'Click me',
        href: '/dashboard',
      },
    });

    const link = container.querySelector('a');
    expect(link).toHaveAttribute('href', '/dashboard');
    expect(link).toHaveClass('hover:shadow-xl', 'hover:scale-105', 'cursor-pointer');
  });

  it('renders as button when onClick provided', async () => {
    const onClick = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '321',
        label: 'Click me',
        onClick,
      },
    });

    const button = container.querySelector('button');
    expect(button).toBeInTheDocument();
    expect(button).toHaveClass('hover:shadow-xl', 'hover:scale-105', 'cursor-pointer');

    if (button) {
      await fireEvent.click(button);
    }
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('renders as static div when no interaction', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '123',
        label: 'Static',
      },
    });

    const div = container.querySelector('div.card');
    expect(div).toBeInTheDocument();
    expect(div).not.toHaveClass('hover:shadow-xl', 'hover:scale-105', 'cursor-pointer');
    expect(container.querySelector('a')).not.toBeInTheDocument();
    expect(container.querySelector('button')).not.toBeInTheDocument();
  });

  it('applies custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '42',
        label: 'Custom',
        className: 'custom-stats-card',
      },
    });

    const card = container.querySelector('.card');
    expect(card).toHaveClass('custom-stats-card');
  });

  it('spreads additional props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(StatsCard as any, {
      props: {
        value: '999',
        label: 'Test',
        id: 'stats-test',
        'data-testid': 'stats-card',
      },
    });

    const card = container.querySelector('.card');
    expect(card).toHaveAttribute('id', 'stats-test');
    expect(card).toHaveAttribute('data-testid', 'stats-card');
  });

  it('does not render icon when loading', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCardTestWrapper as any, {
      props: {
        showIcon: true,
        loading: true,
      },
    });

    expect(screen.queryByTestId('custom-icon')).not.toBeInTheDocument();
  });

  it('renders numeric values correctly', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(StatsCard as any, {
      props: {
        value: 12345,
        label: 'Number Value',
      },
    });

    expect(screen.getByText('12345')).toBeInTheDocument();
  });
});
