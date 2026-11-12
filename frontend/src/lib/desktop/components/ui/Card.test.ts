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

  it.each([
    ['panel', false],
    ['default', true],
  ] as const)('%s variant renders the section title and header actions', (variant, clips) => {
    const { container } = renderTyped(CardTestWrapper, {
      props: { title: 'Storage', variant, actionLabel: 'Refresh' },
    });

    const heading = screen.getByRole('heading', { level: 3, name: 'Storage' });
    const action = screen.getByRole('button', { name: 'Refresh' });
    const body = screen.getByText('Card content');
    // Reading order is title, then actions, then body.
    expect(heading.compareDocumentPosition(action)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(action.compareDocumentPosition(body)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(body).not.toContainElement(action);
    // Only the default card clips; the panel must not cut off sticky headers or popovers.
    const overflowClasses = [...(container.firstElementChild?.classList ?? [])].filter(c =>
      c.startsWith('overflow-')
    );
    expect(overflowClasses).toEqual(clips ? ['overflow-hidden'] : []);
  });

  it('renders header actions next to a custom header', () => {
    renderTyped(CardTestWrapper, {
      props: { variant: 'panel', showHeader: true, actionLabel: 'Refresh' },
    });

    const header = screen.getByText('Custom Header');
    const action = screen.getByRole('button', { name: 'Refresh' });
    expect(header.compareDocumentPosition(action)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
    expect(action.compareDocumentPosition(screen.getByText('Card content'))).toBe(
      Node.DOCUMENT_POSITION_FOLLOWING
    );
  });

  it('panel title and description break long unbroken words', () => {
    const longWord = 'x'.repeat(200);
    renderCard({ variant: 'panel', title: `T${longWord}`, description: `D${longWord}` });

    // The panel does not clip, so text that cannot wrap would run out of it.
    expect(screen.getByRole('heading', { level: 3 })).toHaveClass('wrap-anywhere');
    expect(screen.getByText(`D${longWord}`)).toHaveClass('wrap-anywhere');
  });

  it('renders header actions without a title', () => {
    renderTyped(CardTestWrapper, {
      props: { variant: 'panel', actionLabel: 'Create backup' },
    });

    expect(screen.queryByRole('heading')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Create backup' })).toBeInTheDocument();
  });
  // The body wrapper's classes for each variant, header source and padding setting: each of
  // title, description, header snippet and header actions alone counts as a header, and
  // bodyClassName is always appended.
  it.each([
    ['default', 'none', true, 'p-6'],
    ['default', 'title', true, 'px-6 pb-6'],
    ['default', 'actions', true, 'px-6 pb-6'],
    ['default', 'description', true, 'px-6 pb-6'],
    ['default', 'header', true, 'px-6 pb-6'],
    ['default', 'title', false, ''],
    ['panel', 'none', true, 'flex-1 min-h-0 p-4'],
    ['panel', 'title', true, 'flex-1 min-h-0 px-4 pb-4'],
    ['panel', 'actions', true, 'flex-1 min-h-0 px-4 pb-4'],
    ['panel', 'description', true, 'flex-1 min-h-0 px-4 pb-4'],
    ['panel', 'header', true, 'flex-1 min-h-0 px-4 pb-4'],
    ['panel', 'actions', false, 'flex-1 min-h-0'],
  ] as const)(
    '%s variant with header %s and padding %s gives the body "%s" plus bodyClassName',
    (variant, headerSource, padding, expected) => {
      renderTyped(CardTestWrapper, {
        props: {
          variant,
          padding,
          bodyClassName: 'gap-3',
          title: headerSource === 'title' ? 'Storage' : '',
          actionLabel: headerSource === 'actions' ? 'Refresh' : '',
          description: headerSource === 'description' ? 'Disk usage' : undefined,
          showHeader: headerSource === 'header',
        },
      });

      const body = screen.getByText('Card content');
      const classes = [...body.classList].sort();
      expect(classes).toEqual([...expected.split(' ').filter(Boolean), 'gap-3'].sort());
    }
  );
});
