import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import Card from './Card.svelte';
import CardTestWrapper from './Card.test.svelte';

describe('Card', () => {
  it('renders with default props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(Card as any, {
      props: {},
    });

    const card = container.querySelector('.card');
    expect(card).toBeInTheDocument();
    expect(card).toHaveClass('card', 'bg-base-100', 'shadow-sm');
  });

  it('renders with title', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(Card as any, {
      props: { title: 'Test Card' },
    });

    expect(screen.getByText('Test Card')).toBeInTheDocument();
    expect(screen.getByText('Test Card')).toHaveClass('card-title');
  });

  it('renders with custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(Card as any, {
      props: { className: 'custom-class' },
    });

    const card = container.querySelector('.card');
    expect(card).toHaveClass('custom-class');
  });

  it('renders without padding when padding is false', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(Card as any, {
      props: { padding: false },
    });

    const cardBody = container.querySelector('.card-body');
    expect(cardBody).toHaveClass('p-0');
  });

  it('renders with slots', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CardTestWrapper as any, {
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
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CardTestWrapper as any, {
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
