import { describe, it, expect, beforeEach, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string, params?: Record<string, unknown>) =>
    params ? `${key} ${JSON.stringify(params)}` : key
  ),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({ birdnet: { threshold: 0.8 } });
});

import DetectionStep from './DetectionStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { flushAsync, renderStep } from './stepTestUtils';

/** Seeds the stored (server) threshold the step reads when it mounts. */
function seedThreshold(threshold: number | undefined) {
  settingsStore.update(state => ({
    ...state,
    // Deliberately partial: the step only reads birdnet.threshold
    originalData: { ...state.originalData, birdnet: { threshold } } as unknown as NonNullable<
      typeof state.originalData
    >,
  }));
}

const checkedRadios = () =>
  screen.queryAllByRole('radio').filter(r => r.getAttribute('aria-checked') === 'true');
const radio = (name: RegExp) => screen.getByRole('radio', { name });
const storedLine = () => screen.queryByText(/wizard\.steps\.detection\.descriptionStored/);

describe('DetectionStep - stored threshold', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
    seedThreshold(0.8);
  });

  it.each([
    [0.6, /highSensitivity/],
    [0.7, /balanced/],
    [0.7000000001, /balanced/],
    [0.9, /highAccuracy/],
  ])('checks the preset that equals the stored threshold %s', async (threshold, name) => {
    seedThreshold(threshold);
    renderStep(DetectionStep);
    await flushAsync();

    const checked = checkedRadios();
    expect(checked).toHaveLength(1);
    expect(checked[0]).toHaveAccessibleName(name);
    expect(storedLine()).toBeNull();
    expect(screen.getByText('wizard.steps.detection.description')).toBeInTheDocument();
  });

  it.each([0.75, 0.55, 0.8])(
    'checks no preset and states the stored value when it matches none (%s)',
    async threshold => {
      seedThreshold(threshold);
      renderStep(DetectionStep);
      await flushAsync();

      expect(screen.getAllByRole('radio')).toHaveLength(3);
      expect(checkedRadios()).toHaveLength(0);
      const line = storedLine();
      expect(screen.queryByText('wizard.steps.detection.description')).toBeNull();
      expect(line).toHaveTextContent(JSON.stringify({ threshold }));
      expect(screen.getByRole('radiogroup')).toHaveAttribute('aria-describedby', line?.id);
    }
  );

  it('checks nothing and shows no stored value line when the threshold is not loaded', async () => {
    seedThreshold(undefined);
    renderStep(DetectionStep);
    await flushAsync();

    expect(checkedRadios()).toHaveLength(0);
    expect(storedLine()).toBeNull();
    expect(screen.getByRole('radiogroup')).not.toHaveAttribute('aria-describedby');
  });

  it.each([0.7, 0.9])('saves nothing when the step is left without a change (%s)', async v => {
    seedThreshold(v);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('saves nothing when the step is left untouched with a stored value that matches no preset', async () => {
    seedThreshold(0.75);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();

    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('saves a preset picked over a stored value that matches none, and drops the stored line', async () => {
    seedThreshold(0.75);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );

    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdnet', { threshold: 0.9 });
    expect(storedLine()).toBeNull();
  });

  it('makes the first card the one Tab stop when nothing is checked', async () => {
    seedThreshold(0.75);
    renderStep(DetectionStep);
    await flushAsync();
    const user = userEvent.setup();

    const [first, ...others] = screen.getAllByRole('radio');
    expect(first).toHaveAttribute('tabindex', '0');
    for (const radio of others) {
      expect(radio).toHaveAttribute('tabindex', '-1');
    }
    await user.tab();
    expect(document.activeElement).toBe(first);
    expect(checkedRadios()).toHaveLength(0);
  });

  it('saves nothing when the step is tabbed through with a stored value that matches no preset', async () => {
    seedThreshold(0.75);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    const user = userEvent.setup();

    await user.tab();
    await user.tab();
    await leave();

    expect(checkedRadios()).toHaveLength(0);
    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('arrow keys change the preset and the leave handler saves it', async () => {
    seedThreshold(0.7);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    const user = userEvent.setup();
    await user.tab();
    expect(document.activeElement).toBe(radio(/balanced/));

    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(document.activeElement).toBe(radio(/highAccuracy/)));
    expect(checkedRadios()).toEqual([radio(/highAccuracy/)]);
    await leave();
    await leave();
    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdnet', { threshold: 0.9 });
  });

  it('an arrow key from a stored value that matches no preset checks the next preset and saves it', async () => {
    seedThreshold(0.75);
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    const user = userEvent.setup();
    await user.tab();
    expect(document.activeElement).toBe(radio(/highSensitivity/));
    expect(checkedRadios()).toHaveLength(0);

    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(document.activeElement).toBe(radio(/balanced/)));
    expect(checkedRadios()).toEqual([radio(/balanced/)]);
    await leave();
    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdnet', { threshold: 0.7 });
    expect(storedLine()).toBeNull();
  });

  it('resends the pick after a failed save and not after a successful one', async () => {
    const { leave } = renderStep(DetectionStep);
    await flushAsync();
    await fireEvent.click(
      screen.getByRole('radio', { name: /wizard\.steps\.detection\.highAccuracy/ })
    );
    vi.mocked(settingsActions.saveSection).mockRejectedValueOnce(new Error('offline'));

    await expect(leave()).rejects.toThrow('offline');
    await leave();
    await leave();

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(2);
    expect(settingsActions.saveSection).toHaveBeenLastCalledWith('birdnet', { threshold: 0.9 });
  });
});

// The leave handler contract shared by every step is in stepContract.test.ts
describe('DetectionStep - leave handler', () => {
  beforeEach(() => {
    seedThreshold(0.8);
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
  beforeEach(() => {
    seedThreshold(0.8);
  });

  it('does not colour the Recommended badge text with the primary colour', () => {
    renderStep(DetectionStep);

    const badge = screen.getByText('wizard.steps.detection.balancedRecommended');
    expect(badge).toHaveClass('text-[var(--color-base-content)]');
    expect(badge).not.toHaveClass('text-[var(--color-primary)]');
  });

  it.each([
    { name: 'with a preset checked', threshold: 0.7 },
    { name: 'with no preset checked', threshold: 0.75 },
  ])('has no violations $name', async ({ threshold }) => {
    seedThreshold(threshold);
    const { container } = renderStep(DetectionStep);
    await flushAsync();

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});
