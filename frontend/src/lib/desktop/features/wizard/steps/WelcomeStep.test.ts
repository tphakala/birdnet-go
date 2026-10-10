import { describe, it, expect, vi } from 'vitest';
import { screen } from '@testing-library/svelte';
import { renderTyped } from '../../../../../test/render-helpers';

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
