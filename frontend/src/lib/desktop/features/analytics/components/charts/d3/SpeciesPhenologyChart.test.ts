import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup, fireEvent } from '@testing-library/svelte';
import SpeciesPhenologyChart from './SpeciesPhenologyChart.svelte';
import { ChartTooltip } from './utils/interactions';
import type { PhenologyData } from './utils/phenology';

// The per-visitor dictionary knows only the swift, so the other species keep the payload name.
vi.mock('$lib/stores/speciesDictionary.svelte', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/stores/speciesDictionary.svelte')>()),
  localizeScientific: (scientificName: string) =>
    scientificName === 'Apus apus' ? 'Tervapääsky' : undefined,
}));

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// Three species with overlapping residency spans, in arrival order (as the server returns them).
const sample: PhenologyData = {
  rows: [
    {
      scientificName: 'Apus apus',
      commonName: 'Common Swift',
      firstSeen: '2026-03-01',
      lastSeen: '2026-03-20',
      count: 40,
    },
    {
      scientificName: 'Hirundo rustica',
      commonName: 'Barn Swallow',
      firstSeen: '2026-03-05',
      lastSeen: '2026-03-28',
      count: 25,
    },
    {
      scientificName: 'Delichon urbicum',
      commonName: 'House Martin',
      firstSeen: '2026-03-10',
      lastSeen: '2026-03-25',
      count: 12,
    },
  ],
};

const empty: PhenologyData = { rows: [] };

describe('SpeciesPhenologyChart', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(SpeciesPhenologyChart, { props: { data: empty } })).not.toThrow();
  });

  it('renders the axes and one residency bar per species', async () => {
    const { container } = render(SpeciesPhenologyChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
    expect(container.querySelectorAll('.phenology-bars rect')).toHaveLength(3);
  });

  it('exposes the full common name as a y-axis tick <title> (recoverable when truncated)', async () => {
    // A name longer than LABEL_MAX_CHARS (20) is truncated on the axis; the full name must still be
    // reachable via a native <title> for touch long-press and assistive tech.
    const longName: PhenologyData = {
      rows: [
        {
          scientificName: 'Nycticorax nycticorax',
          commonName: 'Black-crowned Night Heron',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-20',
          count: 9,
        },
        {
          scientificName: 'Setophaga coronata',
          commonName: 'Yellow-rumped Warbler',
          firstSeen: '2026-03-05',
          lastSeen: '2026-03-28',
          count: 7,
        },
      ],
    };
    const { container } = render(SpeciesPhenologyChart, { props: { data: longName, width: 800 } });
    await Promise.resolve();
    const titles = Array.from(container.querySelectorAll('.y-axis .tick title')).map(
      n => n.textContent
    );
    expect(titles).toContain('Black-crowned Night Heron');
    expect(titles).toContain('Yellow-rumped Warbler');
  });

  it('prefers the visitor-locale name over the payload name, like its sibling charts', async () => {
    const { container } = render(SpeciesPhenologyChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    const titles = Array.from(container.querySelectorAll('.y-axis .tick title')).map(
      n => n.textContent
    );
    expect(titles).toEqual(expect.arrayContaining(['Tervapääsky', 'Barn Swallow', 'House Martin']));
    expect(titles).not.toContain('Common Swift');
  });

  it('renders single-day species (collapsed x-domain) with a visible, non-NaN bar', async () => {
    // Every species seen on exactly one (identical) day: first === last for all rows. The +1-day
    // inclusive end keeps the x-domain positive-width, so bars must render with finite x/width and a
    // minimum width rather than a zero-width (invisible) or NaN rect.
    const singleDay: PhenologyData = {
      rows: [
        {
          scientificName: 'Apus apus',
          commonName: 'Common Swift',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-01',
          count: 5,
        },
        {
          scientificName: 'Hirundo rustica',
          commonName: 'Barn Swallow',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-01',
          count: 3,
        },
      ],
    };
    const { container } = render(SpeciesPhenologyChart, { props: { data: singleDay, width: 800 } });
    await Promise.resolve();
    const rects = container.querySelectorAll('.phenology-bars rect');
    expect(rects).toHaveLength(2);
    for (const rect of rects) {
      const x = rect.getAttribute('x') ?? '';
      const width = rect.getAttribute('width') ?? '';
      expect(x).not.toContain('NaN');
      expect(width).not.toContain('NaN');
      expect(Number(width)).toBeGreaterThan(0);
    }
  });

  it('does not draw bars for empty data', async () => {
    const { container } = render(SpeciesPhenologyChart, { props: { data: empty, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('.phenology-bars rect')).toHaveLength(0);
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(SpeciesPhenologyChart, {
      props: { data: sample, ariaLabel: 'Species phenology' },
    });
    expect(container.querySelector('[aria-label="Species phenology"]')).toBeTruthy();
  });

  it('uses a group container so the focusable bars stay in the accessibility tree', () => {
    const { container } = render(SpeciesPhenologyChart, {
      props: { data: sample, ariaLabel: 'Species phenology' },
    });
    const chart = container.querySelector('[aria-label="Species phenology"]');
    expect(chart?.getAttribute('role')).toBe('group');
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(SpeciesPhenologyChart, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('phenology-summary')).toBeTruthy();
  });

  describe('bar details without a mouse', () => {
    let showSpy: ReturnType<typeof vi.spyOn>;
    let hideSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
      showSpy = vi.spyOn(ChartTooltip.prototype, 'show').mockImplementation(() => {});
      hideSpy = vi.spyOn(ChartTooltip.prototype, 'hide').mockImplementation(() => {});
    });

    afterEach(() => {
      showSpy.mockRestore();
      hideSpy.mockRestore();
    });

    async function renderBars(): Promise<{ bars: SVGRectElement[] }> {
      const { container } = render(SpeciesPhenologyChart, { props: { data: sample, width: 800 } });
      await Promise.resolve();
      const bars = Array.from(container.querySelectorAll<SVGRectElement>('.phenology-bars rect'));
      hideSpy.mockClear(); // drawChart hides any stale tooltip before it draws
      return { bars };
    }

    // The Barn Swallow bar: the test file's dictionary mock renders Apus apus as a Finnish name.
    function swallowBar(bars: SVGRectElement[]): SVGRectElement {
      const bar = bars[1];
      expect(bar).toBeDefined();
      return bar;
    }

    it('makes every residency bar keyboard focusable with a descriptive label', async () => {
      const { bars } = await renderBars();
      expect(bars).toHaveLength(3);
      for (const bar of bars) {
        expect(bar.getAttribute('tabindex')).toBe('0');
        expect(bar.getAttribute('role')).toBe('img');
      }
      const label = swallowBar(bars).getAttribute('aria-label') ?? '';
      expect(label).toContain('Barn Swallow');
      expect(label).toContain('2026-03-05');
      expect(label).toContain('2026-03-28');
    });

    it('shows the bar tooltip on keyboard focus', async () => {
      const { bars } = await renderBars();
      await fireEvent.focus(swallowBar(bars));
      expect(showSpy).toHaveBeenCalledOnce();
      const arg = showSpy.mock.calls[0][0] as { title: string; items: unknown[] };
      expect(arg.title).toBe('Barn Swallow');
      expect(arg.items).toHaveLength(4);
    });

    it('hides the bar tooltip on blur and on Escape', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      await fireEvent.focus(bar);
      await fireEvent.blur(bar);
      expect(hideSpy).toHaveBeenCalledTimes(1);
      await fireEvent.focus(bar);
      expect(bar.style.opacity).toBe('1');
      await fireEvent.keyDown(bar, { key: 'Escape' });
      expect(hideSpy).toHaveBeenCalledTimes(2);
      expect(bar.style.opacity).toBe('0.85');
    });

    it('hides the bar tooltip on a pointerdown outside the bars', async () => {
      const { bars } = await renderBars();
      await fireEvent.focus(swallowBar(bars));
      document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
      expect(hideSpy).toHaveBeenCalled();
    });

    it('keeps the bar tooltip on a pointerdown inside the bars', async () => {
      const { bars } = await renderBars();
      await fireEvent.focus(swallowBar(bars));
      bars[0].dispatchEvent(new Event('pointerdown', { bubbles: true }));
      expect(hideSpy).not.toHaveBeenCalled();
    });

    it('hides a hover tooltip when the page scrolls', async () => {
      const { bars } = await renderBars();
      await fireEvent.mouseEnter(swallowBar(bars));
      expect(showSpy).toHaveBeenCalledOnce();
      window.dispatchEvent(new Event('scroll'));
      expect(hideSpy).toHaveBeenCalled();
    });

    it('re-anchors the bar tooltip to the focused bar when the page scrolls', async () => {
      const { bars } = await renderBars();
      swallowBar(bars).focus();
      showSpy.mockClear();
      window.dispatchEvent(new Event('scroll'));
      expect(showSpy).toHaveBeenCalledOnce();
      expect(showSpy.mock.lastCall?.[0]).toMatchObject({ title: 'Barn Swallow' });
      expect(hideSpy).not.toHaveBeenCalled();
    });

    it('hides the bar tooltip when the focused bar scrolls out of the viewport', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      bar.focus();
      showSpy.mockClear();
      vi.spyOn(bar, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, -500, 100, 20));
      window.dispatchEvent(new Event('scroll'));
      expect(showSpy).not.toHaveBeenCalled();
      expect(hideSpy).toHaveBeenCalled();
    });

    it('hides the bar tooltip when the focused bar scrolls out of the viewport sideways', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      bar.focus();
      showSpy.mockClear();
      vi.spyOn(bar, 'getBoundingClientRect').mockReturnValue(
        new DOMRect(window.innerWidth + 50, 10, 100, 20)
      );
      window.dispatchEvent(new Event('scroll'));
      expect(showSpy).not.toHaveBeenCalled();
      expect(hideSpy).toHaveBeenCalled();
    });

    it('keeps the focused bar tooltip when the mouse leaves another bar', async () => {
      const { bars } = await renderBars();
      const focused = swallowBar(bars);
      focused.focus();
      await fireEvent.mouseEnter(bars[0]);
      showSpy.mockClear();
      await fireEvent.mouseLeave(bars[0]);
      expect(hideSpy).not.toHaveBeenCalled();
      expect(showSpy.mock.lastCall?.[0]).toMatchObject({ title: 'Barn Swallow' });
      expect(focused.style.opacity).toBe('1');
      // The tooltip is anchored again, so a scroll keeps following the focused bar.
      showSpy.mockClear();
      window.dispatchEvent(new Event('scroll'));
      expect(showSpy.mock.lastCall?.[0]).toMatchObject({ title: 'Barn Swallow' });
    });

    it('keeps an Escape-dismissed tooltip closed when the mouse leaves another bar', async () => {
      const { bars } = await renderBars();
      const focused = swallowBar(bars);
      focused.focus();
      await fireEvent.mouseEnter(bars[0]);
      await fireEvent.keyDown(focused, { key: 'Escape' });
      showSpy.mockClear();
      await fireEvent.mouseLeave(bars[0]);
      expect(showSpy).not.toHaveBeenCalled();
      // A new tap reopens it.
      await fireEvent.click(focused);
      expect(showSpy.mock.lastCall?.[0]).toMatchObject({ title: 'Barn Swallow' });
    });

    it('keeps a tooltip dismissed by a tap outside closed when the mouse leaves another bar', async () => {
      const { bars } = await renderBars();
      swallowBar(bars).focus();
      document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
      await fireEvent.mouseEnter(bars[0]);
      showSpy.mockClear();
      await fireEvent.mouseLeave(bars[0]);
      expect(showSpy).not.toHaveBeenCalled();
    });

    it('hides a hover tooltip on mouseleave when no bar has focus', async () => {
      const { bars } = await renderBars();
      await fireEvent.mouseEnter(bars[0]);
      await fireEvent.mouseLeave(bars[0]);
      expect(hideSpy).toHaveBeenCalledOnce();
    });

    it('does not reopen a tooltip dismissed with Escape when the page scrolls', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      bar.focus();
      await fireEvent.keyDown(bar, { key: 'Escape' });
      showSpy.mockClear();
      window.dispatchEvent(new Event('scroll'));
      expect(showSpy).not.toHaveBeenCalled();
    });

    it('restores the bar highlight on a pointerdown outside the bars', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      await fireEvent.focus(bar);
      expect(bar.style.opacity).toBe('1');
      document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
      expect(bar.style.opacity).toBe('0.85');
    });

    it('removes its document and window listeners on unmount', async () => {
      const { container, unmount } = render(SpeciesPhenologyChart, {
        props: { data: sample, width: 800 },
      });
      await Promise.resolve();
      const bar = container.querySelectorAll<SVGRectElement>('.phenology-bars rect')[1];
      bar.focus();
      unmount();
      showSpy.mockClear();
      hideSpy.mockClear();
      window.dispatchEvent(new Event('scroll'));
      document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }));
      expect(showSpy).not.toHaveBeenCalled();
      expect(hideSpy).not.toHaveBeenCalled();
    });

    it('shows the bar tooltip on tap (click) and focuses the bar', async () => {
      const { bars } = await renderBars();
      const bar = swallowBar(bars);
      await fireEvent.click(bar);
      // focus() fires the focus handler and the click handler shows again (same content, idempotent).
      expect(showSpy).toHaveBeenCalled();
      expect(showSpy.mock.lastCall?.[0]).toMatchObject({ title: 'Barn Swallow' });
      expect(document.activeElement).toBe(bar);
    });
  });
});
