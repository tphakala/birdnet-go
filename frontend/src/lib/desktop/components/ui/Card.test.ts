import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import Card from './Card.svelte';
import CardTestWrapper from './Card.test.svelte';
import type { ComponentProps } from 'svelte';

// Helper function to render Card with proper typing
const renderCard = (props?: Partial<ComponentProps<typeof Card>>) => {
  return renderTyped(Card, props ? { props } : { props: {} });
};

describe('Card', () => {
  it('renders with default props', () => {
    const { container } = renderCard();

    // Now uses native Tailwind classes with CSS variables
    const card = container.querySelector('.rounded-lg');
    expect(card).toBeInTheDocument();
    expect(card).toHaveClass('rounded-lg', 'overflow-hidden', 'shadow-sm');
    expect(card?.className).toContain('bg-[var(--color-base-100)]');
  });

  it('renders with title', () => {
    renderCard({ title: 'Test Card' });

    expect(screen.getByText('Test Card')).toBeInTheDocument();
    expect(screen.getByText('Test Card')).toHaveClass('text-xl', 'font-semibold');
  });

  it('renders with custom className', () => {
    const { container } = renderCard({ className: 'custom-class' });

    const card = container.querySelector('.rounded-lg');
    expect(card).toHaveClass('custom-class');
  });

  it('renders without padding when padding is false', () => {
    const { container } = renderCard({ padding: false, title: 'Test' });

    // The content div should not have padding classes when padding is false
    const contentDiv = container.querySelector('.rounded-lg > div:last-child');
    expect(contentDiv).toBeInTheDocument();
    expect(contentDiv).not.toHaveClass('px-6', 'pb-6');
  });

  it('renders with slots', () => {
    renderTyped(CardTestWrapper, {
      props: {
        title: 'Card Title',
        showHeader: true,
        showFooter: true,
      },
    });

    expect(screen.getByText('Custom Header')).toBeInTheDocument();
    expect(screen.getByText('Card content')).toBeInTheDocument();
    expect(screen.getByText('Footer content')).toBeInTheDocument();
  });

  it('prefers header slot over title prop', () => {
    renderTyped(CardTestWrapper, {
      props: {
        title: 'Title Prop',
        showHeader: true,
        showFooter: false,
      },
    });

    expect(screen.getByText('Custom Header')).toBeInTheDocument();
    expect(screen.queryByText('Title Prop')).not.toBeInTheDocument();
  });
});
