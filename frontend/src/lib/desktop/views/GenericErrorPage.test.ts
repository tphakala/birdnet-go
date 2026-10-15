import { describe, it, expect, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import GenericErrorPage from './GenericErrorPage.svelte';
import { navigation } from '$lib/stores/navigation.svelte';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

const NO_ACCESS = { enabled: true, accessAllowed: false };

function loginLink(): string | null {
  return screen.getByRole('link', { name: 'common.loginToViewDetails' }).getAttribute('href');
}

describe('GenericErrorPage login link', () => {
  afterEach(() => {
    resetBasePath();
    navigation.redirect('/ui/dashboard');
  });

  it('links to the login route with the current page as the redirect', () => {
    navigation.redirect('/ui/detections?species=Robin');
    render(GenericErrorPage, { props: { code: 500, title: 'Oops', security: NO_ACCESS } });

    expect(loginLink()).toBe('/ui/login?redirect=%2Fui%2Fdetections%3Fspecies%3DRobin');
  });

  it('keeps the reverse-proxy prefix', () => {
    navigation.redirect('/ui/detections?species=Robin');
    setBasePath('/proxy');
    render(GenericErrorPage, { props: { code: 500, title: 'Oops', security: NO_ACCESS } });

    expect(loginLink()).toBe('/proxy/ui/login?redirect=%2Fui%2Fdetections%3Fspecies%3DRobin');
  });
});
