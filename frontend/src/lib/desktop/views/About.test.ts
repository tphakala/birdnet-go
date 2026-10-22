import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

import About from './About.svelte';

describe('About static assets', () => {
  beforeEach(() => {
    // The version fetch is non-critical and its failure is swallowed.
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('offline')));
    setBasePath('/proxy');
  });

  afterEach(() => {
    resetBasePath();
    vi.unstubAllGlobals();
  });

  it('keeps the reverse-proxy prefix on the logo', () => {
    render(About);

    expect(screen.getByRole('img', { name: 'about.logoAlt' })).toHaveAttribute(
      'src',
      '/proxy/ui/assets/BirdNET-Go-logo.webp'
    );
  });

  it('keeps the reverse-proxy prefix on the dependency licenses link', () => {
    render(About);

    expect(screen.getByRole('link', { name: 'about.dependencyLicenses' })).toHaveAttribute(
      'href',
      '/proxy/ui/assets/LICENSES.md'
    );
  });
});
