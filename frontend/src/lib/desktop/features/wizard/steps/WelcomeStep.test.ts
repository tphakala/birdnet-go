import { describe, it, expect, vi, afterEach } from 'vitest';
import { screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

import WelcomeStep from './WelcomeStep.svelte';

describe('WelcomeStep Accessibility', () => {
  it('nests the welcome heading under the dialog title', () => {
    renderTyped(WelcomeStep, { props: {} });

    // The wizard dialog title is an h3, so the step heading is an h4
    expect(
      screen.getByRole('heading', { level: 4, name: 'wizard.steps.welcome.heading' })
    ).toBeInTheDocument();
  });
});

describe('WelcomeStep logo', () => {
  afterEach(() => {
    resetBasePath();
  });

  it('keeps the reverse-proxy prefix', () => {
    setBasePath('/proxy');
    renderTyped(WelcomeStep, { props: {} });

    expect(screen.getByRole('img', { name: 'about.logoAlt' })).toHaveAttribute(
      'src',
      '/proxy/ui/assets/BirdNET-Go-logo.webp'
    );
  });
});
