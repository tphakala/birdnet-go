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
import { expectNoA11yViolations } from '$lib/utils/axe-utils';

// Any 24 ASCII letters and digits
const VALID_TOKEN = 'aB3dEf6hIj9lMn2pQr5tUv8x';

// The token is a plain text input, so a browser does not offer to save it as a password
const TOKEN_LABEL = 'settings.integration.birdweather.token.label';
const tokenInput = () => screen.getByRole('textbox', { name: TOKEN_LABEL });
const findTokenInput = () => screen.findByRole('textbox', { name: TOKEN_LABEL });

// The accessible names of the three cards
const PRIVACY = /wizard\.steps\.integration\.privacyFilterLabel/;
const BIRDWEATHER = /wizard\.steps\.integration\.birdweatherLabel/;
const SENTRY = /wizard\.steps\.integration\.errorReportingLabel/;

// The leave handler contract shared by every step is in stepContract.test.ts
describe('IntegrationStep - leave handler', () => {
  beforeEach(() => {
    vi.mocked(settingsActions.saveSection).mockClear().mockResolvedValue(undefined);
  });

  const clickByName = (name: RegExp) => fireEvent.click(screen.getByRole('checkbox', { name }));

  async function changeAll() {
    await clickByName(PRIVACY);
    await clickByName(BIRDWEATHER);
    const input = await findTokenInput();
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
    expect(screen.getByRole('checkbox', { name: PRIVACY })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: SENTRY })).toBeChecked();
    expect(tokenInput()).toHaveValue(VALID_TOKEN);

    vi.mocked(settingsActions.saveSection).mockClear();
    await leave();

    expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
      ['birdweather', { enabled: true, id: VALID_TOKEN }],
      ['privacyfilter', { enabled: false }],
      ['sentry', { enabled: true }],
    ]);
  });

  describe('with a malformed token while BirdWeather is on', () => {
    async function enterMalformedToken() {
      await clickByName(PRIVACY);
      await clickByName(SENTRY);
      await clickByName(BIRDWEATHER);
      await fireEvent.input(await findTokenInput(), { target: { value: 'abc' } });
    }

    it('Back with a malformed token saves privacy and error reporting and not BirdWeather', async () => {
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await enterMalformedToken();

      await leave();

      expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
        ['privacyfilter', { enabled: false }],
        ['sentry', { enabled: true }],
      ]);
    });

    it('a later Next after fixing the token sends only BirdWeather', async () => {
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await enterMalformedToken();
      await leave();
      vi.mocked(settingsActions.saveSection).mockClear();

      await fireEvent.input(tokenInput(), { target: { value: VALID_TOKEN } });
      await leave();

      expect(vi.mocked(settingsActions.saveSection).mock.calls).toEqual([
        ['birdweather', { enabled: true, id: VALID_TOKEN }],
      ]);
    });

    it('a second leave with the token still malformed sends nothing', async () => {
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await enterMalformedToken();
      await leave();
      vi.mocked(settingsActions.saveSection).mockClear();

      await leave();

      expect(settingsActions.saveSection).not.toHaveBeenCalled();
    });
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
  const initialForm = JSON.stringify(get(settingsStore).formData);

  const toggleBirdweather = () =>
    fireEvent.click(screen.getByRole('checkbox', { name: BIRDWEATHER }));
  const typeToken = (value: string) => fireEvent.input(tokenInput(), { target: { value } });
  const leaveField = () => fireEvent.blur(tokenInput());
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
    expect(tokenInput()).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');

    await leaveField();

    expect(tokenInput()).toHaveAttribute('aria-invalid', 'true');
    expect(alertText()).toBe(ENTER_TOKEN);
  });

  it('a malformed token blocks Next at once but shows the field error only after the field is left', async () => {
    const onValidChange = vi.fn();
    renderStep(IntegrationStep, { onValidChange });
    await flushAsync();
    await toggleBirdweather();

    await typeToken('TESTID123');

    expect(lastReport(onValidChange)).toEqual([false, TOKEN_FORMAT]);
    expect(tokenInput()).not.toHaveAttribute('aria-invalid');
    expect(alertText()).toBe('');

    await leaveField();

    const input = tokenInput();
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
    expect(tokenInput()).not.toHaveAttribute('aria-invalid');
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

    const input = tokenInput();
    expect(input).toBeInstanceOf(HTMLInputElement);
    expect(input).toHaveAttribute('type', 'text');
  });

  it('the token field is a plain text input with no reveal button, so no browser offers to save it as a password', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();
    await typeToken(VALID_TOKEN);

    expect(tokenInput()).toHaveAttribute('type', 'text');
    expect(tokenInput()).toHaveValue(VALID_TOKEN);
    expect(screen.queryByRole('button', { name: 'forms.labels.showPassword' })).toBeNull();
  });

  it('typing in or clicking the token does not toggle BirdWeather', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();

    await fireEvent.input(tokenInput(), { target: { value: 'abc' } });
    await fireEvent.click(tokenInput());

    expect(screen.getByRole('checkbox', { name: BIRDWEATHER })).toBeChecked();
    expect(tokenInput()).toHaveValue('abc');
  });

  it('the token field and its label are outside the BirdWeather card', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();

    const card = screen.getByRole('checkbox', { name: BIRDWEATHER }).closest('label');
    expect(card).not.toBeNull();
    expect(card).not.toContainElement(tokenInput());

    await fireEvent.click(screen.getByText(TOKEN_LABEL));

    expect(screen.getByRole('checkbox', { name: BIRDWEATHER })).toBeChecked();
  });

  it('the token alert keeps its reserved height before and after the error appears', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();

    const alert = screen.getByRole('alert');
    expect(alert).toHaveClass('min-h-10');
    expect(alertText()).toBe('');

    await typeToken('TESTID123');
    await leaveField();

    expect(alertText()).toBe(TOKEN_FORMAT);
    expect(screen.getByRole('alert')).toBe(alert);
    expect(alert).toHaveClass('min-h-10');
  });

  it('renders exactly one alert while BirdWeather is on, with and without an error', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    expect(screen.queryAllByRole('alert')).toHaveLength(0);

    await toggleBirdweather();
    expect(screen.getAllByRole('alert')).toHaveLength(1);

    await leaveField();
    expect(alertText()).toBe(ENTER_TOKEN);
    expect(screen.getAllByRole('alert')).toHaveLength(1);
  });

  it('the token field does not offer saved logins', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    await toggleBirdweather();

    expect(tokenInput()).toHaveAttribute('autocomplete', 'off');
  });

  it('each card colours its icon primary only while on', async () => {
    renderStep(IntegrationStep);
    await flushAsync();
    const icon = (name: RegExp) =>
      screen.getByRole('checkbox', { name }).closest('label')?.querySelector('svg');
    const primary = 'text-[var(--color-primary)]';

    expect(icon(PRIVACY)).toHaveClass(primary);
    expect(icon(BIRDWEATHER)).not.toHaveClass(primary);
    expect(icon(SENTRY)).not.toHaveClass(primary);

    await toggleBirdweather();
    await fireEvent.click(screen.getByRole('checkbox', { name: PRIVACY }));
    await fireEvent.click(screen.getByRole('checkbox', { name: SENTRY }));

    expect(icon(PRIVACY)).not.toHaveClass(primary);
    expect(icon(BIRDWEATHER)).toHaveClass(primary);
    expect(icon(SENTRY)).toHaveClass(primary);
  });

  it.each([
    [PRIVACY, 'wizard.steps.integration.privacyFilterHelp'],
    [BIRDWEATHER, 'wizard.steps.integration.birdweatherHelp'],
    [SENTRY, 'wizard.steps.integration.errorReportingHelp'],
  ])('describes the card %s by its help text, not by a dangling id', async (name, help) => {
    renderStep(IntegrationStep);
    await flushAsync();

    expect(screen.getByRole('checkbox', { name })).toHaveAccessibleDescription(help);
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

    expect(tokenInput()).toHaveAttribute('aria-invalid', 'true');
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
    expect(tokenInput()).toHaveAttribute('aria-invalid', 'true');
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
  describe('with BirdWeather off', () => {
    const savedCalls = () => vi.mocked(settingsActions.saveSection).mock.calls;

    it('BirdWeather off with a malformed token sends nothing', async () => {
      seed({ enabled: false, id: '' });
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await toggleBirdweather();
      await typeToken('abc');
      await toggleBirdweather();

      await leave();

      expect(settingsActions.saveSection).not.toHaveBeenCalled();
    });

    it('BirdWeather off with a malformed token keeps the stored token', async () => {
      seed({ enabled: false, id: VALID_TOKEN });
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await toggleBirdweather();
      await typeToken('abc');
      await toggleBirdweather();
      await fireEvent.click(screen.getByRole('checkbox', { name: PRIVACY }));

      await leave();

      expect(savedCalls()).toEqual([['privacyfilter', { enabled: false }]]);
    });

    it('BirdWeather off with a well-formed new token saves it', async () => {
      seed({ enabled: false, id: '' });
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await toggleBirdweather();
      await typeToken(VALID_TOKEN);
      await toggleBirdweather();

      await leave();

      expect(savedCalls()).toEqual([['birdweather', { enabled: false, id: VALID_TOKEN }]]);
    });

    it('BirdWeather off with an emptied token saves the empty token', async () => {
      seed({ enabled: false, id: VALID_TOKEN });
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await toggleBirdweather();
      await typeToken('');
      await toggleBirdweather();

      await leave();

      expect(savedCalls()).toEqual([['birdweather', { enabled: false, id: '' }]]);
    });

    it('a malformed token typed after a successful save is not sent either', async () => {
      seed({ enabled: false, id: '' });
      const { leave } = renderStep(IntegrationStep);
      await flushAsync();
      await toggleBirdweather();
      await typeToken(VALID_TOKEN);
      await toggleBirdweather();
      await leave();
      vi.mocked(settingsActions.saveSection).mockClear();

      await toggleBirdweather();
      await typeToken('abc');
      await toggleBirdweather();
      await leave();

      expect(settingsActions.saveSection).not.toHaveBeenCalled();
    });

    it('turning BirdWeather on again keeps the typed token and blocks Next, and Back sends no BirdWeather', async () => {
      seed({ enabled: false, id: '' });
      const onValidChange = vi.fn();
      const { leave } = renderStep(IntegrationStep, { onValidChange });
      await flushAsync();
      await toggleBirdweather();
      await typeToken('abc');
      await toggleBirdweather();
      await leave();
      expect(settingsActions.saveSection).not.toHaveBeenCalled();

      await toggleBirdweather();
      expect(tokenInput()).toHaveValue('abc');
      expect(lastReport(onValidChange)).toEqual([false, TOKEN_FORMAT]);
      await leave();

      expect(settingsActions.saveSection).not.toHaveBeenCalled();
    });
  });
});

describe('IntegrationStep Accessibility', () => {
  it('has no axe violations with BirdWeather off', async () => {
    const { container } = renderStep(IntegrationStep);
    await flushAsync();

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });

  it('has no axe violations with BirdWeather on and no error', async () => {
    const { container } = renderStep(IntegrationStep);
    await flushAsync();
    await fireEvent.click(screen.getByRole('checkbox', { name: BIRDWEATHER }));

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });

  it('has no axe violations with the token error shown', async () => {
    const { container } = renderStep(IntegrationStep);
    await flushAsync();
    await fireEvent.click(screen.getByRole('checkbox', { name: BIRDWEATHER }));
    await fireEvent.blur(tokenInput());
    expect(screen.getByRole('alert')).toHaveTextContent(
      'wizard.steps.integration.reasons.enterToken'
    );

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});
