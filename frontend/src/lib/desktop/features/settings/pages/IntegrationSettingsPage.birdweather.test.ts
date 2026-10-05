import { describe, it, expect, beforeEach } from 'vitest';
import { render, fireEvent, screen } from '@testing-library/svelte';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import IntegrationSettingsPage from './IntegrationSettingsPage.svelte';
import { settingsStore, settingsValidationErrors } from '$lib/stores/settings';
import type { SettingsFormData } from '$lib/stores/settings';

const TOKEN = 'aB3dEf6hIj9lMn2pQr5tUv8x';
const BIRDWEATHER_TOKEN_ERROR_KEY = 'birdweather-token-invalid';
const REQUIRED_MESSAGE = 'settings.integration.birdweather.token.errors.required';
const FORMAT_MESSAGE = 'settings.integration.birdweather.token.errors.format';

// Override only realtime.birdweather on the store's own typed defaults, so the rest of
// the form data keeps the shape the page expects.
function seedStore(birdweather: { enabled: boolean; id: string }) {
  settingsStore.update(state => {
    const formData: SettingsFormData = {
      ...state.formData,
      realtime: {
        ...state.formData.realtime,
        birdweather: {
          latitude: 0,
          longitude: 0,
          locationAccuracy: 1000,
          threshold: 0.7,
          debug: false,
          ...birdweather,
        },
      },
    };
    return { ...state, formData, originalData: formData };
  });
}

function tokenInput(): HTMLInputElement {
  return screen.getByLabelText('settings.integration.birdweather.token.label');
}

function enableToggle(): HTMLElement {
  return screen.getByLabelText('settings.integration.birdweather.enable');
}

describe('IntegrationSettingsPage BirdWeather token check', () => {
  beforeEach(() => {
    settingsValidationErrors.set([]);
  });

  it('shows the format error after leaving a malformed token and blocks saving', async () => {
    seedStore({ enabled: false, id: '' });
    render(IntegrationSettingsPage);

    await fireEvent.click(enableToggle());
    await fireEvent.input(tokenInput(), { target: { value: 'abc' } });
    await fireEvent.blur(tokenInput());
    await tick();

    expect(screen.getByText(FORMAT_MESSAGE)).toBeInTheDocument();
    expect(tokenInput()).toHaveAttribute('aria-invalid', 'true');
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('shows the required error as soon as BirdWeather is enabled with an empty token', async () => {
    seedStore({ enabled: false, id: '' });
    render(IntegrationSettingsPage);

    await fireEvent.click(enableToggle());
    await tick();

    expect(screen.getByText(REQUIRED_MESSAGE)).toBeInTheDocument();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('shows the format error while a malformed token is typed, before the field is left', async () => {
    seedStore({ enabled: true, id: 'abc' });
    render(IntegrationSettingsPage);

    await fireEvent.input(tokenInput(), { target: { value: 'abcd' } });
    await tick();

    expect(screen.getByText(FORMAT_MESSAGE)).toBeInTheDocument();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('keeps the required error for an enabled empty token after leaving the field', async () => {
    seedStore({ enabled: false, id: '' });
    render(IntegrationSettingsPage);

    await fireEvent.click(enableToggle());
    await fireEvent.blur(tokenInput());
    await tick();

    expect(screen.getByText(REQUIRED_MESSAGE)).toBeInTheDocument();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('removes the error and the block when the token is corrected', async () => {
    seedStore({ enabled: true, id: 'abc' });
    render(IntegrationSettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);

    await fireEvent.input(tokenInput(), { target: { value: TOKEN } });
    await tick();

    expect(screen.queryByText(FORMAT_MESSAGE)).not.toBeInTheDocument();
    expect(get(settingsValidationErrors)).not.toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('shows the error for a stored malformed token without a blur', async () => {
    seedStore({ enabled: true, id: 'abc' });
    render(IntegrationSettingsPage);
    await tick();

    expect(screen.getByText(FORMAT_MESSAGE)).toBeInTheDocument();
  });

  it('shows no error and no block when BirdWeather is off with a malformed token', async () => {
    seedStore({ enabled: false, id: 'abc' });
    render(IntegrationSettingsPage);
    await tick();

    expect(screen.queryByText(FORMAT_MESSAGE)).not.toBeInTheDocument();
    expect(get(settingsValidationErrors)).not.toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('removes the error and the block when BirdWeather is turned off', async () => {
    seedStore({ enabled: true, id: 'abc' });
    render(IntegrationSettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);

    await fireEvent.click(enableToggle());
    await tick();

    expect(screen.queryByText(FORMAT_MESSAGE)).not.toBeInTheDocument();
    expect(get(settingsValidationErrors)).not.toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('removes the block when the page unmounts and keeps other pages errors', async () => {
    seedStore({ enabled: true, id: 'abc' });
    settingsValidationErrors.set(['other-page-error']);
    const { unmount } = render(IntegrationSettingsPage);
    await tick();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);

    unmount();

    expect(get(settingsValidationErrors)).toEqual(['other-page-error']);
  });

  it('keeps the error shown after turning BirdWeather off and on again', async () => {
    seedStore({ enabled: false, id: '' });
    render(IntegrationSettingsPage);

    await fireEvent.click(enableToggle());
    await fireEvent.input(tokenInput(), { target: { value: 'abc' } });
    await fireEvent.blur(tokenInput());
    await fireEvent.click(enableToggle());
    await fireEvent.click(enableToggle());
    await tick();

    expect(screen.getByText(FORMAT_MESSAGE)).toBeInTheDocument();
    expect(get(settingsValidationErrors)).toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });

  it('stores a pasted token without the whitespace around it', async () => {
    seedStore({ enabled: true, id: '' });
    render(IntegrationSettingsPage);

    await fireEvent.input(tokenInput(), { target: { value: `  ${TOKEN}\n` } });
    await tick();

    expect(get(settingsStore).formData.realtime?.birdweather?.id).toBe(TOKEN);
    expect(get(settingsValidationErrors)).not.toContain(BIRDWEATHER_TOKEN_ERROR_KEY);
  });
});
