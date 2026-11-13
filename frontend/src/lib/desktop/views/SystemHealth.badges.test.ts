import { describe, it, expect, vi, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import SystemHealth from './SystemHealth.svelte';

const mocks = vi.hoisted(() => ({ post: vi.fn() }));

vi.mock('$lib/utils/api', () => ({ api: { post: mocks.post } }));

const CHECK_NAME = 'log_errors';
const CHECK_BUTTON_NAME = /Log Errors/;
const NEUTRAL_FILL = 'var(--color-base-content)';

function report(details: Record<string, unknown>) {
  return {
    id: 'report-1',
    status: 'warning',
    started_at: '2026-01-01T00:00:00Z',
    completed_at: '2026-01-01T00:00:01Z',
    duration_ms: 1000,
    total_checks: 1,
    results: [
      {
        name: CHECK_NAME,
        category: 'logs',
        status: 'warning',
        message: 'errors found',
        duration_ms: 1,
        details,
      },
    ],
    summary: {},
    count_by_status: { warning: 1 },
  };
}

/** Renders the check with these details, opens its row and returns the badge with this text. */
async function openBadge(
  details: Record<string, unknown>,
  badgeText: string
): Promise<HTMLElement> {
  mocks.post.mockResolvedValue(report(details));
  render(SystemHealth);
  fireEvent.click(await screen.findByRole('button', { name: CHECK_BUTTON_NAME }));
  return screen.findByText(badgeText);
}

const levelDetails = (level: string) => ({
  top_errors: [{ component: 'audio', message: 'boom', count: 3, level }],
});

const signalDetails = (signal: Record<string, unknown>) => ({
  recent_events: [{ time: '2026-01-01T00:00:00Z', source: 'audio', delta: 1, metric: 'drops' }],
  ...signal,
});

describe('SystemHealth status badges', () => {
  afterEach(() => {
    cleanup();
    mocks.post.mockReset();
  });

  it.each([
    ['error', 'var(--text-error)', 'var(--color-error)'],
    ['warn', 'var(--text-warning)', 'var(--color-warning)'],
    ['info', NEUTRAL_FILL, NEUTRAL_FILL],
  ])(
    'colours a %s level badge with its text token and tints it with the fill',
    async (level, text, fill) => {
      const badge = await openBadge(levelDetails(level), level);

      expect(badge.style.color).toBe(text);
      expect(badge.style.background).toContain(fill);
    }
  );

  it.each([
    ['sustained', 'health.detail.patternSustained', 'var(--text-error)', 'var(--color-error)'],
    ['transient', 'health.detail.patternTransient', 'var(--text-warning)', 'var(--color-warning)'],
  ])(
    'colours a %s pattern badge with its text token and tints it with the fill',
    async (pattern, label, text, fill) => {
      const badge = await openBadge(signalDetails({ pattern }), label);

      expect(badge.style.color).toBe(text);
      expect(badge.style.background).toContain(fill);
    }
  );

  it.each([
    ['increasing', 'health.detail.velocityIncreasing', 'var(--text-error)', 'var(--color-error)'],
    [
      'decreasing',
      'health.detail.velocityDecreasing',
      'var(--text-success)',
      'var(--color-success)',
    ],
  ])(
    'colours a %s velocity badge with its text token and tints it with the fill',
    async (velocity, label, text, fill) => {
      const badge = await openBadge(signalDetails({ velocity }), label);

      expect(badge.style.color).toBe(text);
      expect(badge.style.background).toContain(fill);
    }
  );
});
