import { describe, it, expect } from 'vitest';
import { renderTyped, createComponentTestFactory, screen } from '../../../../test/render-helpers';
import Badge from './Badge.svelte';
import BadgeTestWrapper from './Badge.test.svelte';

describe('Badge', () => {
  // Create test factory for reusable Badge testing
  const badgeTest = createComponentTestFactory(Badge);

  it('renders with default props', () => {
    badgeTest.render({ text: 'Default Badge' });

    const badge = screen.getByText('Default Badge');
    expect(badge).toBeInTheDocument();
    // Badge now uses native Tailwind classes (inline-flex, items-center, etc.)
    expect(badge).toHaveClass('inline-flex');
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
      const { unmount } = badgeTest.render({ text: `${variant} badge`, variant });

      const badge = screen.getByText(`${variant} badge`);
      // All badges now use native Tailwind classes with CSS variables
      expect(badge).toHaveClass('inline-flex');
      // Check for variant-specific background class based on CSS variable pattern
      const expectedBgPattern =
        variant === 'neutral'
          ? 'bg-[var(--color-base-300)]'
          : variant === 'ghost'
            ? 'bg-black/5'
            : `bg-[var(--color-${variant})]`;
      expect(badge.className).toContain(expectedBgPattern);

      unmount();
    });
  });

  it('renders with different sizes', () => {
    const sizes = ['xs', 'sm', 'md', 'lg'] as const;

    sizes.forEach(size => {
      const { unmount } = badgeTest.render({ text: `${size} size`, size });

      const badge = screen.getByText(`${size} size`);
      // All sizes have base inline-flex class
      expect(badge).toHaveClass('inline-flex');

      unmount();
    });
  });

  it('renders with outline style', () => {
    badgeTest.render({
      text: 'Outlined',
      variant: 'primary',
      outline: true,
    });

    const badge = screen.getByText('Outlined');
    // Outline badges have border and transparent background
    expect(badge.className).toContain('border');
    expect(badge.className).toContain('bg-transparent');
  });

  it('renders with custom className', () => {
    badgeTest.render({
      text: 'Custom',
      className: 'custom-badge-class',
    });

    const badge = screen.getByText('Custom');
    expect(badge).toHaveClass('custom-badge-class');
  });

  it('renders with children snippet', () => {
    renderTyped(BadgeTestWrapper);

    const icon = screen.getByTestId('badge-icon');
    expect(icon).toBeInTheDocument();
    expect(screen.getByText('Status')).toBeInTheDocument();
  });

  it('spreads additional props', () => {
    badgeTest.render({
      text: 'Badge with ID',
      id: 'test-badge',
      'data-testid': 'badge-element',
    });

    const badge = screen.getByTestId('badge-element');
    expect(badge).toHaveAttribute('id', 'test-badge');
  });

  it('combines multiple classes correctly', () => {
    badgeTest.render({
      text: 'Combined',
      variant: 'error',
      size: 'lg',
      outline: true,
      className: 'ml-2',
    });

    const badge = screen.getByText('Combined');
    // Check for combined classes
    expect(badge).toHaveClass('inline-flex', 'ml-2');
    // Outline error badge should have border and transparent bg
    expect(badge.className).toContain('border');
    expect(badge.className).toContain('bg-transparent');
  });

  it('handles ghost variant correctly', () => {
    badgeTest.render({
      text: 'Ghost',
      variant: 'ghost',
    });

    const badge = screen.getByText('Ghost');
    // Ghost variant uses black/5 or white/5 background
    expect(badge.className).toContain('bg-black/5');
    expect(badge.className).not.toContain('bg-transparent');
  });
});
