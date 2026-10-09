import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import CurrentLocationButton from './CurrentLocationButton.svelte';
import { toastActions } from '$lib/stores/toast';
import {
  clearGeolocationGlobals,
  createPosition,
  createPositionError,
  setGeolocation,
  setSecureContext,
} from '../../../../../test/geolocation-fixtures';

const geolocationMock = {
  getCurrentPosition: vi.fn<Geolocation['getCurrentPosition']>(),
  watchPosition: vi.fn<Geolocation['watchPosition']>(),
  clearWatch: vi.fn<Geolocation['clearWatch']>(),
};

const testFactory = createComponentTestFactory(CurrentLocationButton, {
  latitude: 0,
  longitude: 0,
  onLocation: vi.fn(),
});

describe('CurrentLocationButton', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    geolocationMock.getCurrentPosition.mockReset();
    geolocationMock.watchPosition.mockReset();
    geolocationMock.clearWatch.mockReset();
    setSecureContext(true);
    setGeolocation(geolocationMock);
  });

  afterEach(() => {
    cleanup();
    clearGeolocationGlobals();
  });

  it('reports rounded coordinates and accuracy that includes rounding displacement', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1236, 4.9876, 7.6));
    });

    const result = testFactory.render({ onLocation });
    expect(screen.getByText('Automatic location')).toBeInTheDocument();
    expect(
      screen.getByText("Fills the coordinates using this browser's location.")
    ).toBeInTheDocument();

    const button = screen.getByRole('button', { name: 'Use browser location' });
    expect(button).toHaveClass('btn-primary');
    await fireEvent.click(button);

    expect(geolocationMock.getCurrentPosition).toHaveBeenCalledTimes(1);
    expect(geolocationMock.getCurrentPosition).toHaveBeenCalledWith(
      expect.any(Function),
      expect.any(Function),
      {
        enableHighAccuracy: true,
        timeout: 10_000,
        maximumAge: 0,
      }
    );
    expect(onLocation).toHaveBeenCalledWith(52.124, 4.988);
    expect(geolocationMock.watchPosition).not.toHaveBeenCalled();
    expect(toastActions.success).toHaveBeenCalledWith('Browser location detected.');

    await result.rerender({ latitude: 52.124, longitude: 4.988, onLocation });
    expect(screen.getByRole('status')).toHaveTextContent('Estimated accuracy: within 60 m');
    expect(
      screen.queryByText("Fills the coordinates using this browser's location.")
    ).not.toBeInTheDocument();
  });

  it('disables the action and shows loading feedback while the request is pending', async () => {
    geolocationMock.getCurrentPosition.mockImplementationOnce(() => undefined);
    testFactory.render();

    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    const button = screen.getByRole('button', { name: 'Locating...' });
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');
  });

  it('ignores a result after the coordinates are edited', async () => {
    const onLocation = vi.fn();
    let respond: PositionCallback | undefined;
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      respond = success;
    });

    const result = testFactory.render({ latitude: 51, longitude: 5, onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    await result.rerender({ latitude: 51.1, longitude: 5, onLocation });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Use browser location' })).not.toBeDisabled()
    );
    respond?.(createPosition(52.1, 4.3, 10));

    expect(onLocation).not.toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
    expect(toastActions.error).not.toHaveBeenCalled();
  });

  it('ignores a result after coordinate intent starts before the value is committed', async () => {
    const onLocation = vi.fn();
    let respond: PositionCallback | undefined;
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      respond = success;
    });

    const result = testFactory.render({
      latitude: 51,
      longitude: 5,
      coordinateIntentVersion: 0,
      onLocation,
    });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    await result.rerender({
      latitude: 51,
      longitude: 5,
      coordinateIntentVersion: 1,
      onLocation,
    });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Use browser location' })).not.toBeDisabled()
    );
    respond?.(createPosition(52.1, 4.3, 10));

    expect(onLocation).not.toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
  });

  it('ignores a result after saving starts, even when it finishes before the result arrives', async () => {
    const onLocation = vi.fn();
    let respond: PositionCallback | undefined;
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      respond = success;
    });

    const result = testFactory.render({ latitude: 51, longitude: 5, onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    await result.rerender({ latitude: 51, longitude: 5, onLocation, disabled: true });
    await result.rerender({ latitude: 51, longitude: 5, onLocation, disabled: false });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Use browser location' })).not.toBeDisabled()
    );
    respond?.(createPosition(52.1, 4.3, 10));

    expect(onLocation).not.toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
  });

  it('lets a new request supersede an invalidated request', async () => {
    const onLocation = vi.fn();
    const responses: PositionCallback[] = [];
    geolocationMock.getCurrentPosition.mockImplementation(success => {
      responses.push(success);
    });

    const result = testFactory.render({ latitude: 51, longitude: 5, onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    await result.rerender({ latitude: 51.1, longitude: 5, onLocation });
    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Use browser location' })).not.toBeDisabled()
    );
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    responses[0]?.(createPosition(52.1, 4.3, 10));
    expect(onLocation).not.toHaveBeenCalled();

    responses[1]?.(createPosition(53.1234, 5.9876, 6));
    expect(onLocation).toHaveBeenCalledTimes(1);
    expect(onLocation).toHaveBeenCalledWith(53.123, 5.988);
    expect(toastActions.success).toHaveBeenCalledTimes(1);
  });

  it('ignores a late browser response after the control is unmounted', async () => {
    const onLocation = vi.fn();
    let respond: PositionCallback | undefined;
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      respond = success;
    });

    const result = testFactory.render({ onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));
    result.unmount();
    respond?.(createPosition(52.1, 4.3, 10));

    expect(onLocation).not.toHaveBeenCalled();
    expect(toastActions.success).not.toHaveBeenCalled();
  });

  it('hides accuracy when the coordinates are edited after detection', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1, 4.3, 12));
    });

    const result = testFactory.render({ onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));
    await result.rerender({ latitude: 52.1, longitude: 4.3, onLocation });
    expect(screen.getByRole('status')).toBeInTheDocument();

    await result.rerender({ latitude: 52.2, longitude: 4.3, onLocation });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('hides accuracy when newer coordinate intent keeps the same values', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1, 4.3, 12));
    });

    const result = testFactory.render({ coordinateIntentVersion: 0, onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));
    await result.rerender({
      latitude: 52.1,
      longitude: 4.3,
      coordinateIntentVersion: 0,
      onLocation,
    });
    expect(screen.getByRole('status')).toBeInTheDocument();

    await result.rerender({
      latitude: 52.1,
      longitude: 4.3,
      coordinateIntentVersion: 1,
      onLocation,
    });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  describe('on an insecure origin', () => {
    const insecureHelp = 'Needs HTTPS or localhost. Search for a place or use the map instead.';

    it('is unavailable before any click on an insecure origin, stays reachable by Tab and explains why', async () => {
      setSecureContext(false);
      setGeolocation(undefined);
      const onLocation = vi.fn();
      testFactory.render({ onLocation });

      const button = screen.getByRole('button', { name: 'Use browser location' });
      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).not.toBeDisabled();
      expect(button).toHaveAccessibleDescription(insecureHelp);
      expect(screen.getByText(insecureHelp)).toBeVisible();

      const user = userEvent.setup();
      await user.tab();
      expect(button).toHaveFocus();
      await user.keyboard('{Enter}');
      await user.keyboard(' ');
      await user.click(button);

      expect(toastActions.warning).not.toHaveBeenCalled();
      expect(toastActions.error).not.toHaveBeenCalled();
      expect(geolocationMock.getCurrentPosition).not.toHaveBeenCalled();
      expect(onLocation).not.toHaveBeenCalled();
    });

    it('shows the HTTPS explanation in compact mode too', () => {
      setSecureContext(false);
      testFactory.render({ compact: true });

      const button = screen.getByRole('button', { name: 'Use browser location' });
      expect(button).toHaveAttribute('aria-disabled', 'true');
      expect(button).toHaveAccessibleDescription(insecureHelp);
      expect(screen.queryByText('Automatic location')).not.toBeInTheDocument();
    });

    it('shows no HTTPS explanation on a secure origin', () => {
      testFactory.render();

      const button = screen.getByRole('button', { name: 'Use browser location' });
      expect(button).toBeEnabled();
      expect(button).not.toHaveAttribute('aria-disabled');
      expect(button).not.toHaveAttribute('aria-describedby');
      expect(screen.queryByText(insecureHelp)).not.toBeInTheDocument();
    });
  });

  it('reports browsers without geolocation support', async () => {
    setGeolocation(undefined);
    const onLocation = vi.fn();
    testFactory.render({ onLocation });

    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    expect(toastActions.error).toHaveBeenCalledWith('Device location is unsupported.');
    expect(onLocation).not.toHaveBeenCalled();
  });

  it.each([
    [1, 'warning', 'Location permission was denied.'],
    [2, 'error', 'The device could not determine its location.'],
    [3, 'error', 'The location request timed out.'],
    [99, 'error', 'Could not determine the device location.'],
  ] as const)(
    'leaves coordinates untouched for geolocation error %s',
    async (code, type, message) => {
      const onLocation = vi.fn();
      geolocationMock.getCurrentPosition.mockImplementationOnce((_success, failure) => {
        failure?.(createPositionError(code));
      });
      testFactory.render({ latitude: 51, longitude: 5, onLocation });

      await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

      await waitFor(() => expect(screen.getByRole('button')).not.toBeDisabled());
      expect(onLocation).not.toHaveBeenCalled();
      const expectedToast = type === 'warning' ? toastActions.warning : toastActions.error;
      expect(expectedToast).toHaveBeenCalledWith(message);
    }
  );

  it.each([
    ['NaN latitude', Number.NaN, 4.9],
    ['NaN longitude', 52.1, Number.NaN],
    ['latitude above the maximum', 91, 4.9],
    ['latitude below the minimum', -91, 4.9],
    ['longitude above the maximum', 52.1, 181],
    ['longitude below the minimum', 52.1, -181],
  ] as const)(
    'leaves coordinates untouched when the browser returns invalid coordinates (%s)',
    async (_label, latitude, longitude) => {
      const onLocation = vi.fn();
      geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
        success(createPosition(latitude, longitude, 10));
      });
      testFactory.render({ latitude: 51, longitude: 5, onLocation });

      await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

      expect(onLocation).not.toHaveBeenCalled();
      expect(toastActions.error).toHaveBeenCalledWith('Could not determine the device location.');
    }
  );

  it('accepts the coordinates but omits the accuracy readout when accuracy is not finite', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1, 4.3, Number.POSITIVE_INFINITY));
    });

    const result = testFactory.render({ onLocation });
    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    expect(onLocation).toHaveBeenCalledWith(52.1, 4.3);
    expect(toastActions.success).toHaveBeenCalledWith('Browser location detected.');

    // A non-finite accuracy resolves to null, so the accuracy status line stays hidden
    // and the standard help text remains visible.
    await result.rerender({ latitude: 52.1, longitude: 4.3, onLocation });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(
      screen.getByText("Fills the coordinates using this browser's location.")
    ).toBeInTheDocument();
  });

  it('recovers when the browser API throws synchronously', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(() => {
      throw new DOMException('Blocked by policy', 'SecurityError');
    });
    testFactory.render({ onLocation });

    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));

    expect(screen.getByRole('button')).not.toBeDisabled();
    expect(onLocation).not.toHaveBeenCalled();
    expect(toastActions.error).toHaveBeenCalledWith('Could not determine the device location.');
  });

  it('compact hides the label and idle help but keeps the accuracy readout', async () => {
    const onLocation = vi.fn();
    geolocationMock.getCurrentPosition.mockImplementationOnce(success => {
      success(createPosition(52.1236, 4.9876, 7.6));
    });

    const result = testFactory.render({ compact: true, onLocation });
    expect(screen.queryByText('Automatic location')).not.toBeInTheDocument();
    expect(
      screen.queryByText("Fills the coordinates using this browser's location.")
    ).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Use browser location' })).toBeInTheDocument();

    await fireEvent.click(screen.getByRole('button', { name: 'Use browser location' }));
    await result.rerender({ compact: true, latitude: 52.124, longitude: 4.988, onLocation });

    expect(screen.getByRole('status')).toHaveTextContent('Estimated accuracy: within 60 m');
  });
});
