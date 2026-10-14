import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ServerErrorPage from './ServerErrorPage.svelte';

describe('ServerErrorPage login link', () => {
  it('links to the login route with a redirect back, for a visitor without access', () => {
    render(ServerErrorPage, {
      props: { security: { enabled: true, accessAllowed: false } },
    });

    const link = screen.getByRole('link', { name: 'common.loginToViewDetails' });
    expect(link.getAttribute('href')).toMatch(/^\/ui\/login\?redirect=/);
  });
});
