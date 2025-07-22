import { describe, it, expect } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import CollapsibleSection from './CollapsibleSection.svelte';
import CollapsibleSectionTestWrapper from './CollapsibleSection.test.svelte';
import type { ComponentProps } from 'svelte';

// Helper function to render CollapsibleSection with proper typing
const renderCollapsibleSection = (props?: Partial<ComponentProps<CollapsibleSection>>) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  return render(CollapsibleSection as any, props ? { props } : undefined);
};

describe('CollapsibleSection', () => {
  it('renders with title', () => {
    renderCollapsibleSection({
        title: 'Test Section',
    });

    expect(screen.getByText('Test Section')).toBeInTheDocument();
  });

  it('is collapsed by default', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        showContent: true,
      },
    });

    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'false');

    const content = screen.getByText('Test content');
    const contentDiv = content.closest('.collapse-content');
    expect(contentDiv).toHaveAttribute('aria-hidden', 'true');
  });

  it('is expanded when defaultOpen is true', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        defaultOpen: true,
        showContent: true,
      },
    });

    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('aria-expanded', 'true');

    const content = screen.getByText('Test content');
    const contentDiv = content.closest('.collapse-content');
    expect(contentDiv).toHaveAttribute('aria-hidden', 'false');
  });

  it('toggles when clicked', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        showContent: true,
      },
    });

    const button = screen.getByRole('button');

    // Initially collapsed
    expect(button).toHaveAttribute('aria-expanded', 'false');

    // Click to expand
    await fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'true');

    // Click to collapse
    await fireEvent.click(button);
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('toggles with keyboard Enter key', async () => {
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        showContent: true,
      },
    });

    const button = screen.getByRole('button');

    // Focus the button
    button.focus();

    // Press Enter to expand
    await user.keyboard('{Enter}');
    expect(button).toHaveAttribute('aria-expanded', 'true');

    // Press Enter to collapse
    await user.keyboard('{Enter}');
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('toggles with keyboard Space key', async () => {
    const user = userEvent.setup();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        showContent: true,
      },
    });

    const button = screen.getByRole('button');

    // Focus the button
    button.focus();

    // Press Space to expand
    await user.keyboard(' ');
    expect(button).toHaveAttribute('aria-expanded', 'true');

    // Press Space to collapse
    await user.keyboard(' ');
    expect(button).toHaveAttribute('aria-expanded', 'false');
  });

  it('rotates chevron icon when expanded', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test',
      },
    });

    const svg = container.querySelector('svg');

    // Initially not rotated
    expect(svg).not.toHaveClass('rotate-180');

    // Click to expand
    const button = screen.getByRole('button');
    await fireEvent.click(button);

    // Should be rotated
    expect(svg).toHaveClass('rotate-180');
  });

  it('applies custom className', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test',
        className: 'custom-collapse',
      },
    });

    const collapse = container.querySelector('.collapse');
    expect(collapse).toHaveClass('custom-collapse');
  });

  it('applies custom titleClassName', () => {
    renderCollapsibleSection({
        title: 'Test',
        titleClassName: 'custom-title',
    });

    const button = screen.getByRole('button');
    expect(button).toHaveClass('custom-title');
  });

  it('applies custom contentClassName', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test',
        contentClassName: 'custom-content',
      },
    });

    const content = container.querySelector('.collapse-content');
    expect(content).toHaveClass('custom-content');
  });

  it('renders with children content', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(CollapsibleSectionTestWrapper as any, {
      props: {
        showContent: true,
        defaultOpen: true,
      },
    });

    expect(screen.getByText('Test content')).toBeInTheDocument();
    expect(screen.getByText('More content')).toBeInTheDocument();
  });

  it('spreads additional props', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test',
        id: 'test-collapse',
        'data-testid': 'collapse-section',
      },
    });

    const collapse = container.querySelector('.collapse');
    expect(collapse).toHaveAttribute('id', 'test-collapse');
    expect(collapse).toHaveAttribute('data-testid', 'collapse-section');
  });

  it('sets proper ARIA attributes', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test Section',
      },
    });

    const button = screen.getByRole('button');
    const content = container.querySelector('#Test\\ Section-content');

    expect(button).toHaveAttribute('aria-controls', 'Test Section-content');
    expect(content).toHaveAttribute('id', 'Test Section-content');
  });

  it('has hidden checkbox for DaisyUI compatibility', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(CollapsibleSection as any, {
      props: {
        title: 'Test',
      },
    });

    const checkbox = container.querySelector('input[type="checkbox"]');
    expect(checkbox).toBeInTheDocument();
    expect(checkbox).toHaveClass('sr-only');
    expect(checkbox).toHaveAttribute('aria-hidden', 'true');
  });
});
