import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import { getLocale, t } from '$lib/i18n';
import DetailNavBar from './DetailNavBar.svelte';
import type { DetectionNeighbors } from './detailTypes';

const NAV = 'detections.detail.nav';
const SPECIES = 'Blackbird';

const NONE: DetectionNeighbors = {
  detectionId: 5,
  previous: null,
  next: null,
  previousSameSpecies: null,
  nextSameSpecies: null,
};

function renderBar(neighbors: DetectionNeighbors | null, failed = false) {
  return render(DetailNavBar, {
    props: {
      speciesName: SPECIES,
      date: '2026-11-14',
      time: '02:57:03',
      neighbors,
      failed,
      available: true,
    },
  });
}

function stepLinks(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>('.dd-nav-step'));
}

function names(links: HTMLElement[]): string[] {
  return links.map(link => link.getAttribute('aria-label') ?? '');
}

describe('DetailNavBar step links', () => {
  // The shared mock's own behaviour, put back after each test so later suites get it.
  const sharedT = vi.mocked(t).getMockImplementation();

  beforeEach(() => {
    // Keep the placeholders in the name, so links that differ only by a parameter differ here.
    vi.mocked(t).mockImplementation((key: string, params?: Record<string, unknown>) =>
      params ? `${key}|${JSON.stringify(params)}` : key
    );
  });

  afterEach(() => {
    cleanup();
    if (sharedT) vi.mocked(t).mockImplementation(sharedT);
    vi.mocked(getLocale).mockReturnValue('en');
  });

  it('states a loading reason, never an end of the list, while the neighbours are unknown', () => {
    const { container } = renderBar(null);

    const links = stepLinks(container);
    expect(links).toHaveLength(4);
    for (const link of links) {
      expect(link).toHaveAttribute('aria-disabled', 'true');
      expect(link).not.toHaveAttribute('href');
      expect(link.getAttribute('aria-label')).toContain(`${NAV}.stepLoading`);
      expect(link.getAttribute('title')).toBe(link.getAttribute('aria-label'));
    }
    expect(new Set(names(links)).size).toBe(4);
    expect(container.innerHTML).not.toContain('.noOlder');
    expect(container.innerHTML).not.toContain('.noNewer');
    expect(container.querySelector('.dd-nav-groups')).toHaveAttribute('aria-busy', 'true');
  });

  it('names a missing older detection, any and of the species, once the neighbours are known', () => {
    const { container } = renderBar({ ...NONE, next: 6, nextSameSpecies: 7 });

    const links = stepLinks(container);
    expect(links).toHaveLength(4);
    expect(links[0]).toHaveAttribute('aria-label', `${NAV}.noOlder`);
    expect(links[0]).toHaveAttribute('title', `${NAV}.noOlder`);
    expect(links[0]).toHaveAttribute('aria-disabled', 'true');
    expect(links[2].getAttribute('aria-label')).toContain(`${NAV}.noOlderSpecies`);
    expect(links[1]).toHaveAttribute('href', '/ui/detections/6');
    expect(links[3]).toHaveAttribute('href', '/ui/detections/7');
    expect(container.querySelector('.dd-nav-groups')).toHaveAttribute('aria-busy', 'false');
  });

  it('names a missing newer detection, any and of the species, once the neighbours are known', () => {
    const { container } = renderBar({ ...NONE, previous: 4, previousSameSpecies: 3 });

    const links = stepLinks(container);
    expect(links).toHaveLength(4);
    expect(links[1]).toHaveAttribute('aria-label', `${NAV}.noNewer`);
    expect(links[1]).toHaveAttribute('title', `${NAV}.noNewer`);
    expect(links[3].getAttribute('aria-label')).toContain(`${NAV}.noNewerSpecies`);
    expect(links[0]).toHaveAttribute('href', '/ui/detections/4');
    expect(links[2]).toHaveAttribute('href', '/ui/detections/3');
  });

  it('keeps each link distinguishable and adds the failure when the request failed', () => {
    const { container } = renderBar(null, true);

    const links = stepLinks(container);
    expect(links).toHaveLength(4);
    const labels = names(links);
    expect(new Set(labels).size).toBe(4);
    for (const label of labels) expect(label).toContain(`${NAV}.stepFailed`);
    expect(labels[0]).toContain(`${NAV}.older`);
    expect(labels[1]).toContain(`${NAV}.newer`);
    expect(labels[2]).toContain(`${NAV}.olderSpecies`);
    expect(labels[3]).toContain(`${NAV}.newerSpecies`);
    for (const link of links) expect(link).toHaveAttribute('aria-disabled', 'true');
    expect(container.querySelector('.dd-nav-groups')).not.toHaveAttribute('aria-busy', 'true');
  });

  it('adds no visible text for the blocked reasons', () => {
    const { container } = renderBar(null);
    for (const link of stepLinks(container)) expect(link.textContent.trim()).toBe('');
  });

  // Regression: a step by mouse left focus where it was (Safari does not focus a clicked
  // link), so it was lost when the page made the previous detection inert.
  it('takes focus when clicked', async () => {
    const { container } = renderBar({ ...NONE, previous: 4 });
    const older = stepLinks(container)[0];
    expect(older).not.toHaveFocus();

    await fireEvent.click(older);

    expect(older).toHaveFocus();
  });

  it('keeps the same link element mounted across a step', async () => {
    const view = renderBar({ ...NONE, previous: 4 });
    const before = stepLinks(view.container)[0];
    before.focus();

    await view.rerender({ neighbors: null, failed: false });

    const after = stepLinks(view.container);
    expect(after).toHaveLength(4);
    expect(after[0]).toBe(before);
    expect(after[0]).toHaveFocus();
  });
});

describe('DetailNavBar breadcrumb date', () => {
  afterEach(() => {
    cleanup();
    vi.mocked(getLocale).mockReturnValue('en');
  });

  it('shows the date in the UI language, not as the raw ISO date', () => {
    renderBar(null);
    const crumb = document.querySelector('.dd-nav-crumb-time');
    expect(crumb?.textContent).not.toContain('2026-11-14');
    expect(crumb?.textContent).toContain('2026');
    expect(crumb?.textContent).toContain('02:57:03');
  });

  it('follows the locale', () => {
    vi.mocked(getLocale).mockReturnValue('de');
    renderBar(null);
    expect(document.querySelector('.dd-nav-crumb-time')?.textContent).toMatch(/Nov\.? 2026/);
    expect(document.querySelector('.dd-nav-crumb-time')?.textContent).toMatch(/Sa/);
  });
});
