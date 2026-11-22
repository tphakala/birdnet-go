import { describe, it, expect, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import ErrorPage from './ErrorPage.svelte';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

function dashboardLink(): string | null {
  return screen.getByRole('link', { name: 'common.goToDashboard' }).getAttribute('href');
}

describe('ErrorPage dashboard link', () => {
  afterEach(() => {
    resetBasePath();
  });

  it('links to the dashboard', () => {
    render(ErrorPage);

    expect(dashboardLink()).toBe('/ui/dashboard');
  });

  it('keeps the reverse-proxy prefix', () => {
    setBasePath('/proxy');
    render(ErrorPage);

    expect(dashboardLink()).toBe('/proxy/ui/dashboard');
  });
});
