import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import userEvent from '@testing-library/user-event';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';

// Mock dependencies
// Common mocks are now handled globally in src/test/setup.ts
// ($lib/i18n, $lib/utils/security, $lib/utils/logger)

// Mock appState for CSRF token
vi.mock('$lib/stores/appState.svelte', () => ({
  appState: {
    csrfToken: 'mock-csrf-token',
    initialized: true,
    loading: false,
    error: null,
    retryCount: 0,
    version: 'test',
    security: {
      enabled: false,
      accessAllowed: true,
      authConfig: {
        basicEnabled: false,
        googleEnabled: false,
        githubEnabled: false,
        microsoftEnabled: false,
      },
    },
  },
  getCsrfToken: vi.fn().mockReturnValue('mock-csrf-token'),
  isSentryEnabled: () => false,
  refreshCsrfToken: vi.fn().mockResolvedValue(false),
  initApp: vi.fn().mockResolvedValue(true),
}));

// Replace FilterResponseGraph with a fixture that renders its props, so the
// wiring can be asserted without a canvas or a response request
vi.mock('./FilterResponseGraph.svelte', async () => ({
  default: (await import('../../../../../test/fixtures/MockFilterResponseGraph.svelte')).default,
}));

// Mock the icon components to avoid SVG import issues in tests
vi.mock('$lib/desktop/components/ui/LowPassIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('$lib/desktop/components/ui/HighPassIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));
vi.mock('$lib/desktop/components/ui/BandRejectIcon.svelte', () => ({
  default: vi.fn(() => ({ $set: vi.fn(), $destroy: vi.fn(), $on: vi.fn() })),
}));

describe('AudioEqualizerSettings', () => {
  const mockUpdateCallback = vi.fn();

  const defaultEqualizerSettings = {
    enabled: true,
    filters: [],
  };

  beforeEach(() => {
    vi.clearAllMocks();

    // Mock fetch for filter config API
    global.fetch = vi.fn(() =>
      Promise.resolve({
        ok: false, // Force fallback config usage
        json: () => Promise.resolve({}),
      } as unknown as Response)
    );
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('should render with empty filters initially', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    });

    // Should show "New Filter Type" label for the dropdown
    expect(screen.getByText('settings.audio.audioFilters.newFilterType')).toBeInTheDocument();
  });

  it('should display existing HighPass filter', async () => {
    const existingFilter = {
      type: 'HighPass' as const,
      frequency: 100,
      passes: 2, // 24dB attenuation
      q: 0.707,
    };

    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: {
          enabled: true,
          filters: [existingFilter],
        },
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    // Wait for component to load
    await waitFor(() => {
      // The filter type button shows the filter type name
      expect(screen.getByText('HighPass')).toBeInTheDocument();
    });
  });

  it('should render Add Filter button disabled when no filter type selected', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.newFilterType')).toBeInTheDocument();
    });

    // Add Filter button should be disabled when no filter type is selected
    const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
    expect(addButton).toBeDisabled();
  });

  it('should render the enable equalizer checkbox', async () => {
    const { container } = render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: false,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    });

    // Find the checkbox input via container query
    const checkbox = container.querySelector('input[type="checkbox"]');
    expect(checkbox).toBeTruthy();
    expect(checkbox).toBeChecked();
  });

  it('should render disabled state correctly', async () => {
    render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: defaultEqualizerSettings,
        disabled: true,
        onUpdate: mockUpdateCallback,
      },
    });

    await waitFor(() => {
      expect(screen.getByText('settings.audio.audioFilters.enableEqualizer')).toBeInTheDocument();
    });

    // Add Filter button should be disabled when component is disabled
    const addButton = screen.getByText('settings.audio.audioFilters.addFilter');
    expect(addButton).toBeDisabled();
  });

  it('forwards sampleRate and the enabled flag to the graph', async () => {
    const filter = { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 1 };
    const { rerender } = render(AudioEqualizerSettings, {
      props: {
        equalizerSettings: { enabled: true, filters: [filter] },
        onUpdate: mockUpdateCallback,
        sampleRate: 96000,
      },
    });

    const graph = await screen.findByTestId('filter-response-graph');
    expect(graph).toHaveTextContent('enabled=true;filters=1;sampleRate=96000');

    await rerender({ sampleRate: undefined });
    expect(screen.getByTestId('filter-response-graph')).toHaveTextContent(
      'enabled=true;filters=1;sampleRate=none'
    );
  });

  describe('attenuation dropdown names', () => {
    beforeEach(() => {
      Element.prototype.scrollIntoView = vi.fn();
    });

    it('names the attenuation dropdown of an existing filter independently of its value', async () => {
      render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: {
            enabled: true,
            filters: [{ type: 'HighPass' as const, frequency: 100, passes: 2, q: 0.707 }],
          },
          disabled: false,
          onUpdate: mockUpdateCallback,
        },
      });

      const trigger = await screen.findByRole('combobox', { name: 'Attenuation' });
      expect(trigger).toHaveTextContent('settings.audio.audioFilters.attenuationLevels.24db');
    });

    it('names the attenuation dropdown of the new filter independently of its value', async () => {
      const user = userEvent.setup();
      render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: defaultEqualizerSettings,
          disabled: false,
          onUpdate: mockUpdateCallback,
        },
      });

      await user.click(
        await screen.findByRole('combobox', { name: 'settings.audio.audioFilters.newFilterType' })
      );
      await user.click(await screen.findByRole('option', { name: /HighPass/ }));

      const trigger = await screen.findByRole('combobox', { name: 'Attenuation' });
      expect(trigger).toHaveTextContent('settings.audio.audioFilters.attenuationLevels.off');
    });
  });

  describe('new filter form and Q input', () => {
    const NEW_FILTER_TYPE = 'settings.audio.audioFilters.newFilterType';
    const LEVEL_PREFIX = 'settings.audio.audioFilters.attenuationLevels.';

    type EqualizerFilters = ComponentProps<
      typeof AudioEqualizerSettings
    >['equalizerSettings']['filters'];

    type Payload = { filters: Array<Record<string, unknown>> };

    beforeEach(() => {
      Element.prototype.scrollIntoView = vi.fn();
    });

    function lastPayload(): Payload {
      const calls = mockUpdateCallback.mock.calls;
      const last: unknown = calls[calls.length - 1]?.[0];
      return last as Payload;
    }

    async function pickType(user: ReturnType<typeof userEvent.setup>, type: string) {
      await user.click(await screen.findByRole('combobox', { name: NEW_FILTER_TYPE }));
      await user.click(await screen.findByRole('option', { name: new RegExp(type) }));
    }

    function renderEmpty() {
      return render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: defaultEqualizerSettings,
          disabled: false,
          onUpdate: mockUpdateCallback,
        },
      });
    }

    function renderWithFilters(filters: EqualizerFilters) {
      return render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: { enabled: true, filters },
          onUpdate: mockUpdateCallback,
        },
      });
    }

    it('presets a new filter to Off and adds it with passes 0', async () => {
      for (const type of ['LowPass', 'HighPass', 'BandReject']) {
        mockUpdateCallback.mockClear();
        const user = userEvent.setup();
        renderEmpty();

        await pickType(user, type);
        await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

        const added = lastPayload().filters[0];
        expect(added).toMatchObject({ type, passes: 0, q: 0.707 });
        cleanup();
      }
    });

    it('adds a new filter with the attenuation the user picked', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'HighPass');
      await user.click(await screen.findByRole('combobox', { name: 'Attenuation' }));
      await user.click(await screen.findByRole('option', { name: `${LEVEL_PREFIX}24db` }));
      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

      expect(lastPayload().filters[0]).toMatchObject({ type: 'HighPass', passes: 2 });
    });

    it('lists Off and the four attenuation levels', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'LowPass');
      await user.click(await screen.findByRole('combobox', { name: 'Attenuation' }));

      const names = (await screen.findAllByRole('option')).map(o => o.textContent.trim());
      expect(names).toEqual(['off', '12db', '24db', '36db', '48db'].map(k => LEVEL_PREFIX + k));
    });

    it('shows a Q input for LowPass and HighPass but not for BandReject', async () => {
      renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 2, passes: 1 },
        { type: 'HighPass' as const, frequency: 100, q: 0.5, passes: 1 },
        { type: 'BandReject' as const, frequency: 1000, width: 100, q: 0.707, passes: 1 },
      ]);

      const qInputs = await screen.findAllByRole<HTMLInputElement>('spinbutton', {
        name: 'Q Factor',
      });
      expect(qInputs.map(i => i.value)).toEqual(['2', '0.5']);
    });

    it('keeps a Q typed for a new LowPass filter', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'LowPass');
      const q = await screen.findByRole('spinbutton', { name: 'Q Factor' });
      await user.clear(q);
      await user.type(q, '1.5');
      await user.tab();
      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

      expect(lastPayload().filters[0]).toMatchObject({ type: 'LowPass', q: 1.5 });
    });

    it('commits an edited Q on change, clamped to the table range, without touching passes', async () => {
      const user = userEvent.setup();
      renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 0 },
        { type: 'LowPass' as const, frequency: 12000, q: 0.707 },
      ]);

      const [first, second] = await screen.findAllByRole('spinbutton', { name: 'Q Factor' });

      await user.clear(first);
      await user.type(first, '0.5');
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ q: 0.5, passes: 0 });
      const qValues = mockUpdateCallback.mock.calls.map(c => (c[0] as Payload).filters[0].q);
      expect(qValues).not.toContain(0.1);

      await user.clear(first);
      await user.type(first, '50');
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ q: 10, passes: 0 });

      await user.clear(second);
      await user.type(second, '1.2');
      await user.tab();
      expect(lastPayload().filters[1]).toMatchObject({ q: 1.2 });

      for (const call of mockUpdateCallback.mock.calls) {
        const filters = (call[0] as Payload).filters;
        expect(filters[0]?.passes).toBe(0);
        expect(filters[1]).not.toHaveProperty('passes');
      }
    });

    it('shows Off for an existing filter that has no passes value', async () => {
      renderWithFilters([{ type: 'LowPass' as const, frequency: 15000, q: 0.707 }]);

      const trigger = await screen.findByRole('combobox', { name: 'Attenuation' });
      expect(trigger).toHaveTextContent(`${LEVEL_PREFIX}off`);
    });

    // typed: what the user enters. shown: the Q field afterwards (null keeps the value it
    // had before the entry). committed: the Q handed to onUpdate or stored for the new
    // filter (null when the entry is empty and nothing may be committed).
    const Q_ENTRIES = [
      { name: 'clamps a high entry to the upper bound', typed: '50', shown: '10', committed: 10 },
      {
        name: 'clamps a low entry to the lower bound',
        typed: '0.01',
        shown: '0.1',
        committed: 0.1,
      },
      {
        name: 'restores the stored value on an empty entry',
        typed: '',
        shown: null,
        committed: null,
      },
    ];

    type UserSession = ReturnType<typeof userEvent.setup>;

    // The edit row commits through onUpdate at once; the new-filter row only on Add Filter.
    // Each target reports the Q that ended up committed, or undefined when none did.
    const Q_TARGETS = [
      {
        row: 'an existing filter',
        setup: async (_user: UserSession) => {
          renderWithFilters([{ type: 'LowPass' as const, frequency: 15000, q: 0.5, passes: 1 }]);
        },
        committedQ: async (_user: UserSession) =>
          mockUpdateCallback.mock.calls.length > 0 ? lastPayload().filters[0].q : undefined,
        restoredQ: (_before: string) => undefined,
      },
      {
        row: 'a new filter',
        setup: async (user: UserSession) => {
          renderEmpty();
          await pickType(user, 'LowPass');
        },
        committedQ: async (user: UserSession) => {
          await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));
          return lastPayload().filters[0].q;
        },
        restoredQ: (before: string) => Number(before),
      },
    ];

    describe.each(Q_TARGETS)('Q entry for $row', target => {
      it.each(Q_ENTRIES)('$name', async ({ typed, shown, committed }) => {
        const user = userEvent.setup();
        await target.setup(user);

        const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: 'Q Factor' });
        const before = q.value;
        mockUpdateCallback.mockClear();
        await user.clear(q);
        if (typed) await user.type(q, typed);
        await user.tab();

        expect(q.value).toBe(shown ?? before);
        expect(await target.committedQ(user)).toBe(committed ?? target.restoredQ(before));
      });
    });

    it('restores the clamped Q, not the default, when a new filter row gets an empty entry', async () => {
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowPass');

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: 'Q Factor' });
      await user.clear(q);
      await user.type(q, '50');
      await user.tab();
      expect(q.value).toBe('10');

      await user.clear(q);
      await user.tab();
      expect(q.value).toBe('10');

      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));
      expect(lastPayload().filters[0].q).toBe(10);
    });

    it('names the frequency, Q and bandwidth inputs of existing and new filters', async () => {
      const user = userEvent.setup();
      renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 1 },
        { type: 'BandReject' as const, frequency: 1000, width: 100, q: 0.707, passes: 1 },
      ]);

      await screen.findAllByRole('spinbutton', { name: 'Q Factor' });
      const count = (name: string) => screen.queryAllByRole('spinbutton', { name }).length;
      expect([count('Cutoff Frequency'), count('Center Frequency')]).toEqual([1, 1]);
      expect([count('Q Factor'), count('Bandwidth')]).toEqual([1, 1]);

      await pickType(user, 'HighPass');
      expect([count('Cutoff Frequency'), count('Q Factor')]).toEqual([2, 2]);

      await pickType(user, 'BandReject');
      expect([count('Center Frequency'), count('Bandwidth')]).toEqual([2, 2]);
    });
  });
});
