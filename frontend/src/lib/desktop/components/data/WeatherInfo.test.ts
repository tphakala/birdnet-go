import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import WeatherInfo from './WeatherInfo.svelte';
import * as api from '$lib/utils/api';

// Mock the API module
vi.mock('$lib/utils/api', () => ({
  fetchWithCSRF: vi.fn(),
}));

describe('WeatherInfo', () => {
  const mockWeatherData = {
    hourly: {
      temperature: 22.5,
      weatherMain: 'Clear',
      windSpeed: 15,
      humidity: 65,
      pressure: 1013,
      clouds: 20,
    },
    daily: {
      temperatureMin: 18,
      temperatureMax: 26,
      weatherMain: 'Clear',
    },
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders with weather data provided', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        weatherData: mockWeatherData,
      },
    });

    // Note: In test environment, t() returns the translation key
    expect(screen.getByText('detections.weather.title')).toBeInTheDocument();
    expect(screen.getByText('22.5°C')).toBeInTheDocument();
    expect(screen.getByText('Clear')).toBeInTheDocument();
    expect(screen.getByText('15 m/s')).toBeInTheDocument();
    expect(screen.getByText('65%')).toBeInTheDocument();
  });

  it('shows loading state when fetching', async () => {
    const mockFetch = vi.fn().mockImplementation(
      () => new Promise(() => {}) // Never resolves to keep loading state
    );
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('status')).toBeInTheDocument();
      expect(screen.getByText('detections.weather.loading')).toBeInTheDocument();
    });
  });

  it('fetches weather data on mount with detectionId', async () => {
    const mockFetch = vi.fn().mockResolvedValue(mockWeatherData);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
      },
    });

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/weather/detection/123');
      expect(screen.getByText('22.5°C')).toBeInTheDocument();
    });
  });

  it('shows error state when fetch fails', async () => {
    const mockFetch = vi.fn().mockRejectedValue(new Error('Network error'));
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    const onError = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
        onError,
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument();
      expect(screen.getByText('Network error')).toBeInTheDocument();
      expect(onError).toHaveBeenCalledWith(expect.any(Error));
    });
  });

  it('shows error when API returns non-OK response', async () => {
    const mockFetch = vi.fn().mockRejectedValue(new Error('Weather data not available'));
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
      },
    });

    await waitFor(() => {
      expect(screen.getByRole('alert')).toBeInTheDocument();
      expect(screen.getByText('Weather data not available')).toBeInTheDocument();
    });
  });

  it('does not fetch when autoFetch is false', async () => {
    const mockFetch = vi.fn();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
        autoFetch: false,
      },
    });

    await waitFor(() => {
      expect(mockFetch).not.toHaveBeenCalled();
      expect(screen.getByText('detections.weather.noDataAvailable')).toBeInTheDocument();
    });
  });

  it('renders in compact mode', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        weatherData: mockWeatherData,
        compact: true,
      },
    });

    const grid = document.querySelector('[aria-live="polite"]');
    expect(grid).toHaveClass('grid', 'grid-cols-2', 'sm:grid-cols-4');

    // Should not show pressure and clouds in compact mode
    expect(screen.queryByText('detections.weather.labels.pressure')).not.toBeInTheDocument();
    expect(screen.queryByText('detections.weather.labels.cloudCover')).not.toBeInTheDocument();
  });

  it('shows all fields in non-compact mode', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        weatherData: mockWeatherData,
        compact: false,
      },
    });

    expect(screen.getByText('detections.weather.labels.pressure')).toBeInTheDocument();
    // Pressure value is followed by the translated unit key
    expect(screen.getByText(/1013/)).toBeInTheDocument();
    expect(screen.getByText('detections.weather.labels.cloudCover')).toBeInTheDocument();
    expect(screen.getByText('20%')).toBeInTheDocument();
  });

  it('hides title when showTitle is false', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        weatherData: mockWeatherData,
        showTitle: false,
      },
    });

    expect(screen.queryByText('detections.weather.title')).not.toBeInTheDocument();
  });

  it('handles missing data gracefully', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        weatherData: {
          hourly: {
            temperature: undefined,
            weatherMain: undefined,
            windSpeed: undefined,
            humidity: undefined,
          },
        },
      },
    });

    expect(screen.getAllByText('N/A')).toHaveLength(4);
  });

  it('calls onLoad callback when data is fetched', async () => {
    const mockFetch = vi.fn().mockResolvedValue(mockWeatherData);
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    const onLoad = vi.fn();

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    render(WeatherInfo as any, {
      props: {
        detectionId: '123',
        onLoad,
      },
    });

    await waitFor(() => {
      expect(onLoad).toHaveBeenCalledWith(mockWeatherData);
    });
  });

  it('applies custom class names', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(WeatherInfo as any, {
      props: {
        weatherData: mockWeatherData,
        className: 'custom-weather',
        titleClassName: 'custom-title',
        gridClassName: 'custom-grid',
      },
    });

    const weatherDiv = container.querySelector('.weather-info');
    expect(weatherDiv).toHaveClass('custom-weather');

    const title = screen.getByText('detections.weather.title');
    expect(title).toHaveClass('custom-title');

    const grid = document.querySelector('[aria-live="polite"]');
    expect(grid).toHaveClass('custom-grid');
  });

  it('exposes refresh method', async () => {
    const mockFetch = vi.fn().mockResolvedValue({
      ok: true,
      json: vi.fn().mockResolvedValue(mockWeatherData),
    });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { component } = render(WeatherInfo as any, {
      props: {
        detectionId: '123',
        autoFetch: false,
      },
    });

    // Should not fetch initially
    expect(mockFetch).not.toHaveBeenCalled();

    // Call refresh
    component.refresh();

    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/weather/detection/123');
    });
  });

  it('exposes setWeatherData method', async () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { component } = render(WeatherInfo as any, {
      props: {
        detectionId: '123',
        autoFetch: false,
      },
    });

    // Initially no data
    expect(screen.getByText('detections.weather.noDataAvailable')).toBeInTheDocument();

    // Set weather data
    component.setWeatherData(mockWeatherData);

    // Should now display the data
    await waitFor(() => {
      expect(screen.getByText('22.5°C')).toBeInTheDocument();
      expect(screen.getByText('Clear')).toBeInTheDocument();
    });
  });

  it('renders custom content with children snippet', async () => {
    const { default: WeatherInfoTestWrapper } = await import('./WeatherInfo.test.svelte');

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { container } = render(WeatherInfoTestWrapper as any, {
      props: {
        weatherData: mockWeatherData,
        useCustomContent: true,
      },
    });

    expect(container.querySelector('.custom-weather-display')).toBeInTheDocument();
    expect(screen.getByText('Custom: 22.5°C')).toBeInTheDocument();
  });

  it('refetches when detectionId changes', async () => {
    const mockFetch = vi
      .fn()
      .mockResolvedValueOnce(mockWeatherData)
      .mockResolvedValueOnce({
        ...mockWeatherData,
        hourly: { ...mockWeatherData.hourly, temperature: 25 },
      });
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (api.fetchWithCSRF as any).mockImplementation(mockFetch);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const { rerender } = render(WeatherInfo as any, {
      props: {
        detectionId: '123',
      },
    });

    // Wait for first fetch to complete
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/weather/detection/123');
    });

    // Verify initial data is displayed
    await waitFor(() => {
      expect(screen.getByText('22.5°C')).toBeInTheDocument();
    });

    // Clear mock calls to make assertions clearer
    mockFetch.mockClear();

    // Change detectionId
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    await rerender({ detectionId: '456' } as any);

    // Wait for second fetch
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith('/api/v2/weather/detection/456');
    });

    // Wait for updated data to display
    await waitFor(() => {
      expect(screen.getByText('25.0°C')).toBeInTheDocument();
    });
  });

  describe('temperature unit conversion', () => {
    it('displays temperature in Celsius when units is metric', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherInfo as any, {
        props: {
          weatherData: mockWeatherData,
          units: 'metric',
        },
      });

      // 22.5°C should display as 22.5°C (no conversion)
      expect(screen.getByText('22.5°C')).toBeInTheDocument();
    });

    it('converts temperature to Fahrenheit when units is imperial', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherInfo as any, {
        props: {
          weatherData: mockWeatherData,
          units: 'imperial',
        },
      });

      // 22.5°C = 72.5°F (22.5 * 9/5 + 32 = 72.5)
      expect(screen.getByText('72.5°F')).toBeInTheDocument();
    });

    it('converts temperature to Kelvin when units is standard', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherInfo as any, {
        props: {
          weatherData: mockWeatherData,
          units: 'standard',
        },
      });

      // 22.5°C = 295.65K (22.5 + 273.15 = 295.65), displayed with 1 decimal
      expect(screen.getByText('295.6K')).toBeInTheDocument();
    });

    it('reproduces bug fix: 17.2°C displays as ~63°F when units is imperial', () => {
      // This test validates the bug fix from issue #1730
      // Previously, WeatherMetrics showed 17.2 F (Celsius value with F symbol)
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherInfo as any, {
        props: {
          weatherData: {
            hourly: {
              temperature: 17.2, // Bug report value
              weatherMain: 'Clear',
              windSpeed: 5,
              humidity: 50,
            },
          },
          units: 'imperial',
        },
      });

      // 17.2°C = 62.96°F ≈ 63.0°F
      expect(screen.getByText('63.0°F')).toBeInTheDocument();
    });
  });
});
