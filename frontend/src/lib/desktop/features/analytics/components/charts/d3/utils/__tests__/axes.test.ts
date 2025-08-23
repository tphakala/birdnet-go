import { describe, it, expect, beforeEach } from 'vitest';
import { select } from 'd3-selection';
import {
  createHourAxisFormatter,
  createDateAxisFormatter,
  addAxisLabel,
  type AxisTheme,
} from '../axes';

// Mock DOM setup for jsdom
let container: SVGGElement;
let svg: SVGSVGElement;

beforeEach(() => {
  // Clean up any existing DOM
  document.body.innerHTML = '';

  // Create a minimal SVG container
  svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  container = document.createElementNS('http://www.w3.org/2000/svg', 'g');
  svg.appendChild(container);
  document.body.appendChild(svg);
});

describe('createHourAxisFormatter', () => {
  it('should format hours in 24-hour format', () => {
    const formatter = createHourAxisFormatter();

    // Test various hours
    expect(formatter(0)).toBe('00:00');
    expect(formatter(1)).toBe('01:00');
    expect(formatter(12)).toBe('12:00');
    expect(formatter(13)).toBe('13:00');
    expect(formatter(23)).toBe('23:00');
  });

  it('should pad single digit hours with zero', () => {
    const formatter = createHourAxisFormatter();

    expect(formatter(1)).toBe('01:00');
    expect(formatter(9)).toBe('09:00');
  });

  it('should handle edge cases', () => {
    const formatter = createHourAxisFormatter();

    expect(formatter(0)).toBe('00:00');
    expect(formatter(23)).toBe('23:00');
  });
});

describe('createDateAxisFormatter', () => {
  // Fixed dates for deterministic testing
  const testDate = new Date('2024-01-15T14:30:00');
  const anotherDate = new Date('2024-12-25T09:15:00');

  it('should format dates for day range', () => {
    const formatter = createDateAxisFormatter('day');

    expect(formatter(testDate)).toBe('14:30');
    expect(formatter(anotherDate)).toBe('09:15');
  });

  it('should format dates for week range', () => {
    const formatter = createDateAxisFormatter('week');

    expect(formatter(testDate)).toBe('Mon 15');
    expect(formatter(anotherDate)).toBe('Wed 25');
  });

  it('should format dates for month range', () => {
    const formatter = createDateAxisFormatter('month');

    expect(formatter(testDate)).toBe('Jan 15');
    expect(formatter(anotherDate)).toBe('Dec 25');
  });

  it('should format dates for year range', () => {
    const formatter = createDateAxisFormatter('year');

    expect(formatter(testDate)).toBe('Jan 2024');
    expect(formatter(anotherDate)).toBe('Dec 2024');
  });

  it('should use default format for unknown range', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const formatter = createDateAxisFormatter('unknown' as any);

    expect(formatter(testDate)).toBe('Jan 15');
    expect(formatter(anotherDate)).toBe('Dec 25');
  });
});

describe('addAxisLabel', () => {
  const mockTheme: AxisTheme = {
    color: '#333',
    fontSize: '12px',
    fontFamily: 'Arial',
    strokeWidth: 1,
    gridColor: '#ccc',
  };

  const baseConfig = {
    text: 'Test Label',
    offset: 20,
    width: 100,
    height: 50,
  };

  beforeEach(() => {
    // Clear any existing labels
    container.innerHTML = '';
  });

  it('should add label at bottom orientation', () => {
    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'bottom',
      },
      mockTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label).toBeTruthy();
    expect(label.textContent).toBe('Test Label');
    expect(label.getAttribute('text-anchor')).toBe('middle');
    expect(label.getAttribute('transform')).toBe('translate(50,70) rotate(0)');
    expect(label.getAttribute('aria-hidden')).toBe('true');

    // Check styles
    expect(label.style.fill).toBe('#333');
    expect(label.style.fontSize).toBe('12px');
    expect(label.style.fontFamily).toBe('Arial');
    expect(label.style.fontWeight).toBe('bold');
    expect(label.style.pointerEvents).toBe('none');
  });

  it('should add label at left orientation with rotation', () => {
    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'left',
      },
      mockTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label).toBeTruthy();
    expect(label.getAttribute('transform')).toBe('translate(-20,25) rotate(-90)');
    expect(label.getAttribute('aria-hidden')).toBe('true');
    expect(label.style.pointerEvents).toBe('none');
  });

  it('should add label at top orientation', () => {
    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'top',
      },
      mockTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label).toBeTruthy();
    expect(label.getAttribute('transform')).toBe('translate(50,-20) rotate(0)');
    expect(label.getAttribute('aria-hidden')).toBe('true');
    expect(label.style.pointerEvents).toBe('none');
  });

  it('should add label at right orientation with rotation', () => {
    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'right',
      },
      mockTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label).toBeTruthy();
    expect(label.getAttribute('transform')).toBe('translate(120,25) rotate(90)');
    expect(label.getAttribute('aria-hidden')).toBe('true');
    expect(label.style.pointerEvents).toBe('none');
  });

  it('should apply all theme styles correctly', () => {
    const customTheme: AxisTheme = {
      color: '#ff0000',
      fontSize: '16px',
      fontFamily: 'Times New Roman',
      strokeWidth: 2,
      gridColor: '#999',
    };

    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'bottom',
      },
      customTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label.style.fill).toBe('#ff0000');
    expect(label.style.fontSize).toBe('16px');
    expect(label.style.fontFamily).toBe('Times New Roman');
  });

  it('should be non-interactive and hidden from screen readers', () => {
    const selection = select(container);

    addAxisLabel(
      selection,
      {
        ...baseConfig,
        orientation: 'bottom',
      },
      mockTheme
    );

    const label = container.querySelector('.axis-label') as SVGTextElement;

    expect(label.getAttribute('aria-hidden')).toBe('true');
    expect(label.style.pointerEvents).toBe('none');
  });
});
