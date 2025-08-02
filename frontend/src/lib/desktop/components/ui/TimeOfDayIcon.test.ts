import { describe, it, expect } from 'vitest';
import { renderTyped } from '../../../../test/render-helpers';
import TimeOfDayIcon from './TimeOfDayIcon.svelte';

describe('TimeOfDayIcon', () => {
  it('renders day icon for daytime hours', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T14:00:00'), // 2 PM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-yellow-500');
    // Check for sun icon path
    expect(svg?.innerHTML).toContain('M16 12a4 4 0 11-8 0 4 4 0 018 0z');
  });

  it('renders night icon for nighttime hours', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T22:00:00'), // 10 PM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-indigo-500');
    // Check for moon icon path
    expect(svg?.innerHTML).toContain('M20.354 15.354A9 9 0 018.646 3.646');
  });

  it('renders sunrise icon for morning hours', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T07:30:00'), // 7:30 AM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-orange-500');
    // Check for sunrise icon elements
    expect(svg?.innerHTML).toContain('polyline points="8 6 12 2 16 6"');
  });

  it('renders sunset icon for evening hours', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T17:30:00'), // 5:30 PM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-red-500');
    // Check for sunset icon elements
    expect(svg?.innerHTML).toContain('polyline points="16 5 12 9 8 5"');
  });

  it('renders dawn icon as sunrise', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T06:30:00'), // 6:30 AM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-orange-400'); // dawn color
    // Should use sunrise icon
    expect(svg?.innerHTML).toContain('polyline points="8 6 12 2 16 6"');
  });

  it('renders dusk icon as sunset', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T18:30:00'), // 6:30 PM
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-purple-500'); // dusk color
    // Should use sunset icon
    expect(svg?.innerHTML).toContain('polyline points="16 5 12 9 8 5"');
  });

  it('uses provided timeOfDay over calculated value', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T14:00:00'), // Would be day
        timeOfDay: 'night', // Override
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-indigo-500'); // night color
  });

  it('renders with different sizes', () => {
    const sizes = [
      { size: 'sm', class: 'h-4 w-4' },
      { size: 'md', class: 'h-5 w-5' },
      { size: 'lg', class: 'h-6 w-6' },
      { size: 'xl', class: 'h-8 w-8' },
    ] as const;

    sizes.forEach(({ size, class: expectedClass }) => {
      const { container, unmount } = renderTyped(TimeOfDayIcon, {
        props: {
          timeOfDay: 'day',
          size,
        },
      });

      const svg = container.querySelector('svg');
      expect(svg).toHaveClass(expectedClass);
      unmount();
    });
  });

  it('shows tooltip when showTooltip is true', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        timeOfDay: 'sunrise',
        showTooltip: true,
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('title', 'Sunrise');
  });

  it('does not show tooltip by default', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        timeOfDay: 'day',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).not.toHaveAttribute('title');
  });

  it('applies custom className', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        timeOfDay: 'day',
        className: 'custom-icon-class',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('custom-icon-class');
  });

  it('spreads additional SVG attributes', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        timeOfDay: 'day',
        'data-testid': 'time-icon',
        role: 'img',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveAttribute('data-testid', 'time-icon');
    expect(svg).toHaveAttribute('role', 'img');
  });

  it('handles string datetime', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: '2024-01-01T14:00:00',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-yellow-500'); // day
  });

  it('handles timestamp datetime', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        datetime: new Date('2024-01-01T22:00:00').getTime(),
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-indigo-500'); // night
  });

  it('defaults to day icon when no datetime or timeOfDay provided', () => {
    const { container } = renderTyped(TimeOfDayIcon);

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-yellow-500');
  });

  it('renders clock icon for unknown timeOfDay', () => {
    const { container } = renderTyped(TimeOfDayIcon, {
      props: {
        // Testing invalid timeOfDay
        timeOfDay: 'unknown' as 'day' | 'night' | 'sunrise' | 'sunset' | 'dawn' | 'dusk',
      },
    });

    const svg = container.querySelector('svg');
    expect(svg).toHaveClass('text-gray-400');
    // Check for clock icon path
    expect(svg?.innerHTML).toContain('M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z');
  });
});
