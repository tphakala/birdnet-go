import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({ birdnet: { threshold: 0.8 } });
});

import DetectionStep from './DetectionStep.svelte';
import { settingsActions } from '$lib/stores/settings';
import { flushAsync, renderStep } from './stepTestUtils';

// The leave handler contract shared by every step is in stepContract.test.ts
describe('DetectionStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  it('the leave handler patches only birdnet threshold', async () => {
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );

    await leave();
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdnet', { threshold: 0.9 });
  });
});

describe('DetectionStep Accessibility', () => {
  it('does not colour the Recommended badge text with the primary colour', () => {
    renderStep(DetectionStep);

    const badge = screen.getByText('wizard.steps.detection.balancedRecommended');
    expect(badge).toHaveClass('text-[var(--color-base-content)]');
    expect(badge).not.toHaveClass('text-[var(--color-primary)]');
  });
});
