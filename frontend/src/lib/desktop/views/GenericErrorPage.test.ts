import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import GenericErrorPage from './GenericErrorPage.svelte';

describe('GenericErrorPage login link', () => {
  it('links to the login route with a redirect back, for a visitor without access', () => {
    render(GenericErrorPage, {
      props: { code: 500, title: 'Oops', security: { enabled: true, accessAllowed: false } },
    });

    const link = screen.getByRole('link', { name: 'common.loginToViewDetails' });
    expect(link.getAttribute('href')).toMatch(/^\/ui\/login\?redirect=/);
  });
});
