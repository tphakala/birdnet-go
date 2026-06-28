import { render, screen } from '@testing-library/svelte';
import { describe, it, expect } from 'vitest';
import ExternalLinkBadge from './ExternalLinkBadge.svelte';

describe('ExternalLinkBadge', () => {
  it('renders the link name and href', () => {
    render(ExternalLinkBadge, { link: { name: 'Wikipedia', url: 'https://x', icon: 'wikipedia' } });
    const a = screen.getByRole('link', { name: /Wikipedia/ });
    expect(a).toHaveAttribute('href', 'https://x');
    expect(a).toHaveAttribute('target', '_blank');
    expect(a).toHaveAttribute('rel', 'noopener noreferrer');
  });

  it('renders for an unknown icon without crashing (generic fallback)', () => {
    render(ExternalLinkBadge, {
      link: { name: 'Mystery', url: 'https://y', icon: 'totally-unknown' },
    });
    expect(screen.getByRole('link', { name: /Mystery/ })).toBeInTheDocument();
  });

  it('renders when icon is omitted', () => {
    render(ExternalLinkBadge, { link: { name: 'NoIcon', url: 'https://z' } });
    expect(screen.getByRole('link', { name: /NoIcon/ })).toBeInTheDocument();
  });
});
