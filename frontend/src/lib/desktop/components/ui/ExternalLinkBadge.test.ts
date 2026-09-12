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

  it.each([
    ['javascript', 'javascript:alert(1)'],
    ['data', 'data:text/html,<script>alert(1)</script>'],
    ['relative (no protocol)', '/species/x'],
    ['empty', ''],
  ])('suppresses the badge for a non-http(s) URL: %s', (_label, url) => {
    render(ExternalLinkBadge, { link: { name: 'Evil', url, icon: 'wikipedia' } });
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
    expect(screen.queryByText('Evil')).not.toBeInTheDocument();
  });

  it('still renders a plain http URL (non-TLS home network sources)', () => {
    render(ExternalLinkBadge, { link: { name: 'Http', url: 'http://example.org/a' } });
    expect(screen.getByRole('link', { name: /Http/ })).toHaveAttribute(
      'href',
      'http://example.org/a'
    );
  });
});
