import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import WeatherMetrics from './WeatherMetrics.svelte';

describe('WeatherMetrics', () => {
  const baseProps = {
    weatherIcon: '01d',
    weatherDescription: 'Clear sky',
    temperature: 20, // 20°C stored internally
    windSpeed: 5,
    timeOfDay: 'day' as const,
  };

  describe('temperature unit conversion', () => {
    it('displays temperature in Celsius when units is metric', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          units: 'metric',
        },
      });

      // 20°C should display as 20.0°C (no conversion)
      expect(screen.getByText('20.0°C')).toBeInTheDocument();
    });

    it('converts temperature to Fahrenheit when units is imperial', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          units: 'imperial',
        },
      });

      // 20°C = 68°F (20 * 9/5 + 32 = 68)
      expect(screen.getByText('68.0°F')).toBeInTheDocument();
    });

    it('converts temperature to Kelvin when units is standard', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          units: 'standard',
        },
      });

      // 20°C = 293.15K (20 + 273.15), displayed with 1 decimal
      expect(screen.getByText('293.1K')).toBeInTheDocument();
    });

    it('defaults to metric (Celsius) when units is not specified', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: baseProps,
      });

      // Should default to Celsius
      expect(screen.getByText('20.0°C')).toBeInTheDocument();
    });

    it('correctly converts negative temperatures to Fahrenheit', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          temperature: -10, // -10°C
          units: 'imperial',
        },
      });

      // -10°C = 14°F (-10 * 9/5 + 32 = 14)
      expect(screen.getByText('14.0°F')).toBeInTheDocument();
    });

    it('correctly converts fractional temperatures', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          temperature: 17.2, // 17.2°C (the bug report case)
          units: 'imperial',
        },
      });

      // 17.2°C = 63.0°F (17.2 * 9/5 + 32 = 62.96, rounded to 63.0)
      expect(screen.getByText('63.0°F')).toBeInTheDocument();
    });

    it('handles zero temperature correctly', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          temperature: 0, // 0°C
          units: 'imperial',
        },
      });

      // 0°C = 32°F
      expect(screen.getByText('32.0°F')).toBeInTheDocument();
    });
  });

  describe('wind speed unit conversion', () => {
    it('displays wind speed in m/s when units is metric', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          units: 'metric',
        },
      });

      // 5 m/s should display as 5 m/s (no conversion)
      expect(screen.getByText(/5.*m\/s/)).toBeInTheDocument();
    });

    it('converts wind speed to mph when units is imperial', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          windSpeed: 5, // 5 m/s
          units: 'imperial',
        },
      });

      // 5 m/s = 11.18 mph ≈ 11 mph (rounded)
      expect(screen.getByText(/11.*mph/)).toBeInTheDocument();
    });

    it('correctly converts wind gust to mph when units is imperial', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          windSpeed: 5, // 5 m/s = 11 mph
          windGust: 10, // 10 m/s = 22 mph
          units: 'imperial',
        },
      });

      // Should show wind speed with gust in parentheses: "11(22) mph"
      expect(screen.getByText(/11.*\(22\).*mph/)).toBeInTheDocument();
    });

    it('displays wind speed in m/s when units is standard', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          ...baseProps,
          units: 'standard',
        },
      });

      // Standard units use m/s for wind speed
      expect(screen.getByText(/5.*m\/s/)).toBeInTheDocument();
    });
  });

  describe('basic rendering', () => {
    it('renders weather icon and description', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: baseProps,
      });

      // Weather emoji should be visible (☀️ for '01d')
      const weatherIcon = screen.getByLabelText(/Clear sky/);
      expect(weatherIcon).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('does not render temperature when undefined', () => {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      render(WeatherMetrics as any, {
        props: {
          weatherIcon: '01d',
          weatherDescription: 'Clear sky',
          windSpeed: 5,
          timeOfDay: 'day' as const,
          // temperature intentionally omitted
        },
      });

      // Should not find any temperature display - look for the temperature group
      expect(screen.queryByText(/\d+\.\d°[CF]|\d+\.\dK/)).not.toBeInTheDocument();
    });
  });
});
