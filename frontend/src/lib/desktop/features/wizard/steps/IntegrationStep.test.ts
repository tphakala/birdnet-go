import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/svelte';

vi.mock('$lib/i18n', () => ({
  t: vi.fn((key: string) => key),
  getLocale: vi.fn(() => 'en'),
}));

vi.mock('$lib/stores/settings', async () => {
  const { createSettingsMock } = await import('./stepTestUtils');
  return createSettingsMock({
    realtime: { privacyFilter: { enabled: true }, birdweather: { enabled: false, id: '' } },
    sentry: { enabled: false },
  });
});

import IntegrationStep from './IntegrationStep.svelte';
import { settingsActions, settingsStore } from '$lib/stores/settings';
import { ApiError } from '$lib/utils/api';
import { get } from 'svelte/store';
import { flushAsync, renderStep } from './stepTestUtils';

// Any 24 ASCII letters and digits
const VALID_TOKEN = 'aB3dEf6hIj9lMn2pQr5tUv8x';

// The leave handler contract shared by every step is in stepContract.test.ts
describe('IntegrationStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  const clickByName = (name: RegExp) => fireEvent.click(screen.getByRole('button', { name }));
  const PRIVACY = /wizard\.steps\.integration\.privacyFilterLabel/;
  const BIRDWEATHER = /wizard\.steps\.integration\.birdweatherLabel/;
  const SENTRY = /wizard\.steps\.integration\.errorReportingLabel/;

  async function changeAll() {
    await clickByName(PRIVACY);
    await clickByName(BIRDWEATHER);
    const input = await screen.findByRole('textbox');
    await fireEvent.input(input, { target: { value: VALID_TOKEN } });
    await clickByName(SENTRY);
  }

  it('the leave handler patches birdweather, privacyfilter and sentry in order', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await leave();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: VALID_TOKEN }],
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });

  it('the leave handler patches only the sections that changed', async () => {
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await clickByName(SENTRY);

    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['sentry', { enabled: true }],
    ]);
  });

  it('sends no further sections once the step unmounted during a save', async () => {
    let resolveSave: () => void = () => {};
    vi.mocked(settingsActions.saveSection).mockImplementationOnce(
      () =>
        new Promise<void>(resolve => {
          resolveSave = resolve;
        })
    );
    const { leave, unmount } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    const pending = leave();
    await flushAsync();
    unmount();
    resolveSave();
    await pending;

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: VALID_TOKEN }],
    ]);
  });

  it('a failed birdweather save sends nothing else', async () => {
    vi.mocked(settingsActions.saveSection).mockRejectedValueOnce(new Error('bad token'));
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await expect(leave()).rejects.toThrow('bad token');

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(settingsActions.saveSection).toHaveBeenCalledWith('birdweather', {
      enabled: true,
      id: VALID_TOKEN,
    });
  });

  it('a server refusal of the birdweather save keeps every choice and a retry sends all sections', async () => {
    const refusal = new ApiError('refused', 400, new Response(null, { status: 400 }));
    vi.mocked(settingsActions.saveSection).mockRejectedValueOnce(refusal);
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await expect(leave()).rejects.toBe(refusal);

    expect(settingsActions.saveSection).toHaveBeenCalledTimes(1);
    expect(screen.getByRole('button', { name: PRIVACY })).toHaveAttribute('aria-pressed', 'false');
    expect(screen.getByRole('button', { name: SENTRY })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('textbox')).toHaveValue(VALID_TOKEN);

    vi.mocked(settingsActions.saveSection).mockClear();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: VALID_TOKEN }],
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });

  it('a retry after a failed privacyfilter save sends only privacyfilter and sentry', async () => {
    vi.mocked(settingsActions.saveSection)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('privacy failed'));
    const { leave } = renderStep(IntegrationStep);
    await flushAsync();
    await changeAll();

    await expect(leave()).rejects.toThrow('privacy failed');
    vi.mocked(settingsActions.saveSection).mockClear();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });
});

describe('IntegrationStep - BirdWeather token', () => {
  const TOKEN_FORMAT = 'wizard.steps.integration.reasons.tokenFormat';
  const ENTER_TOKEN = 'wizard.steps.integration.reasons.enterToken';
  const BIRDWEATHER = /wizard\.steps\.integration\.birdweatherLabel/;
  const initialForm = JSON.stringify(get(settingsStore).formData);

  const toggleBirdweather = () =>
    fireEvent.click(screen.getByRole('button', { name: BIRDWEATHER }));
  const typeToken = (value: string) =>
    fireEvent.input(screen.getByRole('textbox'), { target: { value } });
  const leaveField = () => fireEvent.blur(screen.getByRole('textbox'));
  const alertText = () => screen.getByRole('alert').textContent;

  function seed(birdweather: { enabled: boolean; id: string }) {
    settingsStore.update(state => ({
      ...state,
      formData: {
        ...state.formData,
        realtime: {
          ...state.formData.realtime,
          birdweather: {
            latitude: 0,
            longitude: 0,
            locationAccuracy: 0,
            threshold: 0,
            debug: false,
            ...birdweather,
          },
        },
      },
    }));
  }

  function lastReport(spy: ReturnType<typeof vi.fn>) {
    return spy.mock.calls.at(-1);
  }

  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  afterEach(() => {
    settingsStore.update(state => ({ ...state, formData: JSON.parse(initialForm) }));
  });

  it('reports valid at mount while BirdWeather is off', async () => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();

    expect(lastReport(onValidChange)).toEqual([true, undefined]);
  });

  it('turning BirdWeather on with no token blocks Next with the enter-token reason and shows the field error only after the field is left', async () => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();

    await toggleBirdweather();

    expect(lastReport(onValidChange)).toEqual([false, ENTER_TOKEN]);
    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');

    await leaveField();

    expect(screen.getByRole('textbox')).toHaveAttribute('aria-invalid', 'true');
    expect(alertText()).toBe(ENTER_TOKEN);
  });

  it('a malformed token blocks Next at once but shows the field error only after the field is left', async () => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();
    await toggleBirdweather();

    await typeToken('TESTID123');

    expect(lastReport(onValidChange)).toEqual([false, TOKEN_FORMAT]);
    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');

    await leaveField();

    const input = screen.getByRole('textbox');
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(alertText()).toBe(TOKEN_FORMAT);
    expect(input.getAttribute('aria-describedby')).toContain(screen.getByRole('alert').id);

    await typeToken('TESTID1234');

    expect(input).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');
  });

  it('a 24-character token makes the step valid', async () => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();
    await toggleBirdweather();

    await typeToken(VALID_TOKEN);
    await leaveField();

    expect(lastReport(onValidChange)).toEqual([true, undefined]);
    expect(screen.getByRole('textbox')).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');
  });

  it('the leave handler sends the token trimmed', async () => {
    const onValidChange = vi.fn();
    const { leave } = renderStep(IntegrationStep, { onValidChange });
    await flushAsync();
    await toggleBirdweather();

    await typeToken(`  ${VALID_TOKEN} \n`);
    expect(lastReport(onValidChange)).toEqual([true, undefined]);
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: VALID_TOKEN }],
    ]);
  });

  it("the token field is labelled with the settings page's name", async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();

    expect(screen.getByLabelText('settings.integration.birdweather.token.label')).toBe(
      screen.getByRole('textbox')
    );
  });

  type Action = ['toggle'] | ['type', string] | ['blur'];
  const VALID_MINUS_ONE = VALID_TOKEN.slice(0, -1);
  it.each<[string, Array<[Action, boolean, string | undefined]>]>([
    [
      'on, malformed, off, on again',
      [
        [['toggle'], false, ENTER_TOKEN],
        [['type', 'TESTID123'], false, TOKEN_FORMAT],
        [['blur'], false, TOKEN_FORMAT],
        [['toggle'], true, undefined],
        [['toggle'], false, TOKEN_FORMAT],
      ],
    ],
    [
      'on, valid, one character deleted, retyped',
      [
        [['toggle'], false, ENTER_TOKEN],
        [['type', VALID_TOKEN], true, undefined],
        [['type', VALID_MINUS_ONE], false, TOKEN_FORMAT],
        [['type', VALID_TOKEN], true, undefined],
      ],
    ],
    [
      'on, valid, cleared, off',
      [
        [['toggle'], false, ENTER_TOKEN],
        [['type', VALID_TOKEN], true, undefined],
        [['type', ''], false, ENTER_TOKEN],
        [['toggle'], true, undefined],
      ],
    ],
  ])('validity follows the sequence: %s', async (_name, sequence) => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();

    for (const [action, valid, reason] of sequence) {
      if (action[0] === 'toggle') await toggleBirdweather();
      else if (action[0] === 'type') await typeToken(action[1]);
      else await leaveField();

      expect(lastReport(onValidChange)).toEqual([valid, reason]);
    }
  });

  it('a field error that was showing is still shown when BirdWeather is turned back on', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();
    await typeToken('TESTID123');
    await leaveField();
    await toggleBirdweather();
    await toggleBirdweather();

    expect(screen.getByRole('textbox')).toHaveAttribute('aria-invalid', 'true');
  });

  it('a saved valid token keeps the step valid and the leave handler sends nothing', async () => {
    seed({ enabled: true, id: VALID_TOKEN });
    const onValidChange = vi.fn();
    const { leave } = renderStep(IntegrationStep, { onValidChange });
    await flushAsync();

    expect(lastReport(onValidChange)).toEqual([true, undefined]);
    await leave();

    expect(settingsActions.saveSection).not.toHaveBeenCalled();
  });

  it('a saved malformed token shows its error as soon as BirdWeather is turned on', async () => {
    seed({ enabled: false, id: 'short' });
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();
    expect(lastReport(onValidChange)).toEqual([true, undefined]);

    await toggleBirdweather();

    expect(lastReport(onValidChange)).toEqual([false, TOKEN_FORMAT]);
    expect(screen.getByRole('textbox')).toHaveAttribute('aria-invalid', 'true');
  });

  it('the first validity report already reflects the saved settings', async () => {
    seed({ enabled: true, id: VALID_TOKEN });
    const validSpy = vi.fn();
    const first = renderStep(IntegrationStep, { onValidChange: validSpy });
    await flushAsync();
    expect(validSpy.mock.calls.map(call => call[0])).not.toContain(false);
    first.unmount();

    seed({ enabled: true, id: '' });
    const invalidSpy = vi.fn();
    renderStep(IntegrationStep, { onValidChange: invalidSpy });
    await flushAsync();
    expect(invalidSpy.mock.calls.map(call => call[0])).not.toContain(true);
  });
});
