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
      const { unmount } = badgeTest.render({ text: `${variant} badge`, variant });

      const badge = screen.getByText(`${variant} badge`);
      // neutral variant has only 'badge' class, others have 'badge-{variant}'
      const expectedClass = variant === 'neutral' ? 'badge' : `badge-${variant}`;
      expect(badge).toHaveClass(expectedClass);

      unmount();
    });
  });

  it('renders with different sizes', () => {
    const sizes = ['xs', 'sm', 'md', 'lg'] as const;

    sizes.forEach(size => {
      const { unmount } = badgeTest.render({ text: `${size} size`, size });

      const badge = screen.getByText(`${size} size`);
      // All sizes have 'badge' class
      expect(badge).toHaveClass('badge');
      // md size has no additional class, others have 'badge-{size}'
      const hasSizeClass = size !== 'md';
      expect(badge.classList.contains(`badge-${size}`)).toBe(hasSizeClass);

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
    expect(badge).toHaveClass('badge-primary', 'badge-outline');
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
    expect(badge).toHaveClass('badge', 'badge-error', 'badge-outline', 'badge-lg', 'ml-2');
  });

  it('handles ghost variant correctly', () => {
    badgeTest.render({
      text: 'Ghost',
      variant: 'ghost',
    });

    const badge = screen.getByText('Ghost');
    expect(badge).toHaveClass('badge-ghost');
    expect(badge).not.toHaveClass('badge-outline');
  });
});
