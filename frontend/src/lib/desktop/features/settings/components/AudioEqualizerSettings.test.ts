import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor, cleanup } from '@testing-library/svelte';
import type { ComponentProps } from 'svelte';
import userEvent from '@testing-library/user-event';
import { t } from '$lib/i18n';
import { DEFAULT_EQ_Q } from '$lib/utils/audio/equalizer';
import AudioEqualizerSettings from './AudioEqualizerSettings.svelte';
import fallbackEqFilterConfig from './eqFilterFallbackConfig.json';

// Largest Q the editor accepts, from the fallback table (pinned to the backend table by a
// Go test), and an entry far above it
const Q_MAX = fallbackEqFilterConfig.LowPass.Parameters.find(p => p.Name === 'Q')?.Max ?? NaN;
const Q_ABOVE_MAX = String(Q_MAX * 10);

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

// The global t mock returns the key, so accessible names are keys plus the unit
const KEY = 'settings.audio.audioFilters.';
const NAME = {
  cutoff: `${KEY}cutoffFrequency (Hz)`,
  center: `${KEY}centerFrequency (Hz)`,
  q: `${KEY}qFactor`,
  width: `${KEY}bandwidth (Hz)`,
  attenuation: `${KEY}attenuation`,
};
const NOTICE = {
  min: 'components.forms.numberField.adjustedToMinimum',
  max: 'components.forms.numberField.adjustedToMaximum',
  empty: `${KEY}emptyEntryRestored`,
};

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

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      expect(trigger).toHaveTextContent('settings.audio.audioFilters.attenuationLevels.24db');
    });

    it('focuses the attenuation dropdown without opening it when its label is clicked', async () => {
      const user = userEvent.setup();
      render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: {
            enabled: true,
            filters: [{ type: 'HighPass' as const, frequency: 100, passes: 2, q: 0.707 }],
          },
          onUpdate: mockUpdateCallback,
        },
      });

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      const label = document.querySelector(`label[for="${trigger.id}"]`);
      if (!(label instanceof HTMLLabelElement)) throw new Error('attenuation label not found');

      await user.click(label);

      expect(trigger).toHaveFocus();
      expect(trigger).toHaveAttribute('aria-expanded', 'false');
      expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
      expect(mockUpdateCallback).not.toHaveBeenCalled();
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

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      expect(trigger).toHaveTextContent('settings.audio.audioFilters.attenuationLevels.off');
    });
  });

  describe('new filter form and parameter inputs', () => {
    const NEW_FILTER_TYPE = 'settings.audio.audioFilters.newFilterType';
    const LEVEL_PREFIX = 'settings.audio.audioFilters.attenuationLevels.';

    type EqualizerFilters = ComponentProps<
      typeof AudioEqualizerSettings
    >['equalizerSettings']['filters'];

    type Payload = { filters: Array<Record<string, unknown>> };
    type UserSession = ReturnType<typeof userEvent.setup>;

    beforeEach(() => {
      Element.prototype.scrollIntoView = vi.fn();
    });

    function lastPayload(): Payload {
      const calls = mockUpdateCallback.mock.calls;
      const last: unknown = calls[calls.length - 1]?.[0];
      return last as Payload;
    }

    async function pickType(user: UserSession, type: string) {
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

    // The notice element an input points at through aria-describedby, if any
    function noticeOf(input: HTMLElement): HTMLElement | null {
      const id = input.getAttribute('aria-describedby');
      return id ? document.getElementById(id) : null;
    }

    // Text of every notice element in the editor that currently shows something
    function shownNotices(): string[] {
      return Array.from(document.querySelectorAll('[role="status"][id$="-notice"]'))
        .map(n => n.textContent.trim())
        .filter(text => text !== '');
    }

    it('presets a new filter to Off and adds it with passes 0', async () => {
      for (const type of ['LowPass', 'HighPass', 'BandReject']) {
        mockUpdateCallback.mockClear();
        const user = userEvent.setup();
        renderEmpty();

        await pickType(user, type);
        await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

        const added = lastPayload().filters[0];
        expect(added).toMatchObject({ type, passes: 0, q: DEFAULT_EQ_Q });
        cleanup();
      }
    });

    it('adds a new filter with the attenuation the user picked', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'HighPass');
      await user.click(await screen.findByRole('combobox', { name: NAME.attenuation }));
      await user.click(await screen.findByRole('option', { name: `${LEVEL_PREFIX}24db` }));
      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

      expect(lastPayload().filters[0]).toMatchObject({ type: 'HighPass', passes: 2 });
    });

    it('lists Off and the four attenuation levels', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'LowPass');
      await user.click(await screen.findByRole('combobox', { name: NAME.attenuation }));

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
        name: NAME.q,
      });
      expect(qInputs.map(i => i.value)).toEqual(['2', '0.5']);
    });

    it('gives the Q input a step that keeps the default 0.707 a valid entry', async () => {
      const user = userEvent.setup();
      renderWithFilters([{ type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 1 }]);
      await pickType(user, 'HighPass');

      const qInputs = await screen.findAllByRole<HTMLInputElement>('spinbutton', {
        name: NAME.q,
      });
      expect(qInputs).toHaveLength(2);
      for (const q of qInputs) {
        expect(q).toHaveAttribute('step', '0.001');
        expect(q.value).toBe('0.707');
        expect(q.validity.stepMismatch).toBe(false);
      }
    });

    it.each([
      { name: NAME.q, typed: '0.7071', shown: '0.707', field: 'q', value: 0.707 },
      { name: NAME.cutoff, typed: '1000.5', shown: '1001', field: 'frequency', value: 1001 },
    ])(
      'commits $typed on the step grid as $shown, so the input stays valid',
      async ({ name, typed, shown, field, value }) => {
        const user = userEvent.setup();
        renderWithFilters([STORED.LowPass]);

        const input = await screen.findByRole<HTMLInputElement>('spinbutton', { name });
        await user.clear(input);
        await user.type(input, typed);
        await user.tab();

        expect(input.value).toBe(shown);
        expect(input.validity.valid).toBe(true);
        expect(lastPayload().filters[0]).toHaveProperty(field, value);
        expect(input).not.toHaveAttribute('aria-describedby');
      }
    );

    it('keeps a Q typed for a new LowPass filter', async () => {
      const user = userEvent.setup();
      renderEmpty();

      await pickType(user, 'LowPass');
      const q = await screen.findByRole('spinbutton', { name: NAME.q });
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

      const [first, second] = await screen.findAllByRole('spinbutton', { name: NAME.q });

      await user.clear(first);
      await user.type(first, '0.5');
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ q: 0.5, passes: 0 });
      const qValues = mockUpdateCallback.mock.calls.map(c => (c[0] as Payload).filters[0].q);
      expect(qValues).not.toContain(0.1);

      await user.clear(first);
      await user.type(first, Q_ABOVE_MAX);
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ q: Q_MAX, passes: 0 });

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

    it('shows a stored passes outside the offered levels as its own option and keeps it while Q is edited', async () => {
      const user = userEvent.setup();
      const storedPasses = 8;
      renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: storedPasses },
      ]);

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      expect(trigger).toHaveTextContent(`${LEVEL_PREFIX}custom`);
      expect(t).toHaveBeenCalledWith(`${LEVEL_PREFIX}custom`, { db: storedPasses * 12 });

      const q = await screen.findByRole('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, '1.5');
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ q: 1.5, passes: storedPasses });
    });

    it('drops the custom attenuation option once a standard level is picked', async () => {
      const user = userEvent.setup();
      const view = renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 8 },
      ]);

      await user.click(await screen.findByRole('combobox', { name: NAME.attenuation }));
      expect(await screen.findByRole('option', { name: `${LEVEL_PREFIX}custom` })).toBeVisible();
      await user.click(screen.getByRole('option', { name: `${LEVEL_PREFIX}24db` }));
      expect(lastPayload().filters[0]).toMatchObject({ passes: 2 });

      // The parent feeds the update back; the stored level is only offered while it is current
      await view.rerender({
        equalizerSettings: {
          enabled: true,
          filters: [{ type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 2 }],
        },
        onUpdate: mockUpdateCallback,
      });
      await user.click(await screen.findByRole('combobox', { name: NAME.attenuation }));
      expect(await screen.findByRole('option', { name: `${LEVEL_PREFIX}24db` })).toBeVisible();
      expect(
        screen.queryByRole('option', { name: `${LEVEL_PREFIX}custom` })
      ).not.toBeInTheDocument();
    });

    it('does not offer a custom attenuation option for a stored passes the list has', async () => {
      renderWithFilters([STORED.LowPass]);

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      expect(trigger).toHaveTextContent(`${LEVEL_PREFIX}12db`);
      expect(t).not.toHaveBeenCalledWith(`${LEVEL_PREFIX}custom`, expect.anything());
    });

    it('shows a stored value below the table minimum until its field is committed', async () => {
      const user = userEvent.setup();
      const { min } = bounds('LowPass', 'Frequency');
      const storedFrequency = min / 2;
      renderWithFilters([
        { type: 'LowPass' as const, frequency: storedFrequency, q: 0.5, passes: 1 },
      ]);

      const frequency = await screen.findByRole<HTMLInputElement>('spinbutton', {
        name: NAME.cutoff,
      });
      expect(frequency.value).toBe(String(storedFrequency));

      // Editing another field leaves the stored frequency alone
      const q = await screen.findByRole('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, '1.5');
      await user.tab();
      expect(lastPayload().filters[0]).toMatchObject({ frequency: storedFrequency, q: 1.5 });
    });

    it('keeps a filter of an unknown type as stored and offers no parameters for it', async () => {
      // 'Notch' is a type the backend stores but the component's filter type does not name
      const notch = {
        type: 'Notch',
        frequency: 1000,
        passes: 1,
      } as unknown as EqualizerFilters[number];
      renderWithFilters([notch]);

      expect(await screen.findByText('Notch')).toBeInTheDocument();
      expect(screen.queryAllByRole('spinbutton')).toHaveLength(0);
    });

    it('shows Off for an existing filter that has no passes value', async () => {
      renderWithFilters([{ type: 'LowPass' as const, frequency: 15000, q: 0.707 }]);

      const trigger = await screen.findByRole('combobox', { name: NAME.attenuation });
      expect(trigger).toHaveTextContent(`${LEVEL_PREFIX}off`);
    });

    // One entry per numeric parameter, with the filter type that shows it and its bounds
    // from the fallback table (pinned to the backend table by a Go test).
    function bounds(type: 'LowPass' | 'BandReject', name: string) {
      const table =
        type === 'LowPass' ? fallbackEqFilterConfig.LowPass : fallbackEqFilterConfig.BandReject;
      const param = table.Parameters.find(p => p.Name === name);
      if (!param) throw new Error(`no ${name} parameter for ${type}`);
      return { min: param.Min, max: param.Max };
    }

    const PARAMS = [
      {
        param: 'Frequency',
        field: 'frequency',
        type: 'LowPass' as const,
        label: NAME.cutoff,
        ...bounds('LowPass', 'Frequency'),
      },
      {
        param: 'Q',
        field: 'q',
        type: 'LowPass' as const,
        label: NAME.q,
        ...bounds('LowPass', 'Q'),
      },
      {
        param: 'Width',
        field: 'width',
        type: 'BandReject' as const,
        label: NAME.width,
        ...bounds('BandReject', 'Width'),
      },
    ];

    const STORED: Record<'LowPass' | 'BandReject', EqualizerFilters[number]> = {
      LowPass: { type: 'LowPass', frequency: 12000, q: 0.5, passes: 1 },
      BandReject: { type: 'BandReject', frequency: 1000, width: 200, passes: 1 },
    };

    function stored(type: 'LowPass' | 'BandReject'): EqualizerFilters[number] {
      return type === 'LowPass' ? STORED.LowPass : STORED.BandReject;
    }

    // typed: what the user enters. shown: the field afterwards (null keeps the value it
    // had before the entry). committed: the value handed to onUpdate or stored for the
    // new filter (null when the entry is empty and nothing may be committed). notice:
    // the key of the notice the field then shows.
    function entries(min: number, max: number) {
      return [
        {
          name: 'clamps a high entry to the upper bound',
          typed: String(max * 10),
          shown: String(max),
          committed: max,
          notice: NOTICE.max,
          noticeValue: max,
        },
        {
          name: 'clamps a low entry to the lower bound',
          typed: String(min / 10),
          shown: String(min),
          committed: min,
          notice: NOTICE.min,
          noticeValue: min,
        },
        {
          name: 'restores the stored value on an empty entry',
          typed: '',
          shown: null,
          committed: null,
          notice: NOTICE.empty,
          noticeValue: null,
        },
      ];
    }

    type Param = (typeof PARAMS)[number];

    // The edit row commits through onUpdate at once; the new-filter row only on Add Filter.
    const ROWS = [
      {
        row: 'an existing filter',
        setup: async (_user: UserSession, p: Param) => {
          renderWithFilters([stored(p.type)]);
        },
        committed: async (_user: UserSession, p: Param) =>
          mockUpdateCallback.mock.calls.length > 0 ? lastPayload().filters[0][p.field] : undefined,
        restored: (_before: string) => undefined,
      },
      {
        row: 'a new filter',
        setup: async (user: UserSession, p: Param) => {
          renderEmpty();
          await pickType(user, p.type);
        },
        committed: async (user: UserSession, p: Param) => {
          await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));
          return lastPayload().filters[0][p.field];
        },
        restored: (before: string) => Number(before),
      },
    ];

    const CASES = PARAMS.flatMap(p =>
      ROWS.flatMap(r =>
        entries(p.min, p.max).map(e => ({ ...e, p, r, label: `${p.param}, ${r.row}` }))
      )
    );

    describe.each(CASES)(
      '$label: $name',
      ({ p, r, typed, shown, committed, notice, noticeValue }) => {
        it('commits on change and shows the notice', async () => {
          const user = userEvent.setup();
          await r.setup(user, p);

          const input = await screen.findByRole<HTMLInputElement>('spinbutton', { name: p.label });
          const before = input.value;
          mockUpdateCallback.mockClear();
          vi.mocked(t).mockClear();
          await user.clear(input);
          if (typed) await user.type(input, typed);
          await user.tab();

          expect(input.value).toBe(shown ?? before);
          expect(noticeOf(input)?.textContent.trim()).not.toBe('');
          expect(noticeOf(input)).toHaveAttribute('role', 'status');
          // Clamp notices name the bound; the kept notice names no value, the field shows it
          const noticeArgs = noticeValue === null ? [notice] : [notice, { value: noticeValue }];
          expect(t).toHaveBeenCalledWith(...noticeArgs);
          expect(input).not.toHaveAttribute('aria-invalid');
          expect(await r.committed(user, p)).toBe(committed ?? r.restored(before));
        });
      }
    );

    it('restores the clamped Q, not the default, when a new filter row gets an empty entry', async () => {
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowPass');

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, Q_ABOVE_MAX);
      await user.tab();
      expect(q.value).toBe(String(Q_MAX));

      await user.clear(q);
      await user.tab();
      expect(q.value).toBe(String(Q_MAX));

      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));
      expect(lastPayload().filters[0].q).toBe(Q_MAX);
    });

    it.each([
      { param: 'frequency', type: 'LowPass' as const, label: NAME.cutoff },
      { param: 'width', type: 'BandReject' as const, label: NAME.width },
    ])(
      'does not commit a $param while it is typed, and commits 2000 once on change',
      async ({ param, type, label }) => {
        const user = userEvent.setup();
        renderWithFilters([stored(type)]);

        const input = await screen.findByRole<HTMLInputElement>('spinbutton', { name: label });
        await user.clear(input);
        await user.type(input, '2000');
        expect(mockUpdateCallback).not.toHaveBeenCalled();
        expect(input.value).toBe('2000');

        await user.tab();
        expect(mockUpdateCallback).toHaveBeenCalledTimes(1);
        expect(lastPayload().filters[0]).toHaveProperty(param, 2000);
        expect(input.value).toBe('2000');
        expect(input).not.toHaveAttribute('aria-describedby');
      }
    );

    it('adds a new filter with the frequency committed on change', async () => {
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowPass');

      const frequency = await screen.findByRole<HTMLInputElement>('spinbutton', {
        name: NAME.cutoff,
      });
      await user.clear(frequency);
      await user.type(frequency, '2000');
      await user.tab();
      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));

      expect(mockUpdateCallback).toHaveBeenCalledTimes(1);
      expect(lastPayload().filters[0]).toMatchObject({ type: 'LowPass', frequency: 2000 });
    });

    it('reserves each row a notice band holding the longest notice the row can show', async () => {
      renderWithFilters([STORED.LowPass]);
      await screen.findAllByRole('spinbutton', { name: NAME.q });

      const ghosts = document.querySelectorAll('[data-eq-notice-reserve]');
      // One filter row; the new-filter row has no type picked, so it has no band yet
      expect(ghosts).toHaveLength(1);
      const ghost = ghosts[0];
      expect(ghost).toHaveAttribute('aria-hidden', 'true');
      // Of the min, max and kept notices, the kept one is the longest here
      expect(ghost.textContent.trim()).toBe(NOTICE.empty);
      expect(shownNotices()).toEqual([]);
    });

    it('keeps the reserved band for a stored value of any length', async () => {
      const user = userEvent.setup();
      renderWithFilters([{ type: 'LowPass', frequency: 15000, q: 0.7071067811865476, passes: 1 }]);

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      const reserve = document.querySelector('[data-eq-notice-reserve]')?.textContent.trim() ?? '';
      await user.clear(q);
      await user.tab();

      expect(q.value).toBe('0.7071067811865476');
      const shown = noticeOf(q)?.textContent.trim() ?? '';
      expect(shown).not.toBe('');
      expect(shown.length).toBeLessThanOrEqual(reserve.length);
      expect(shown).not.toContain('0.7071067811865476');
      // The kept notice is the same text for every stored value
      const keptCalls = vi.mocked(t).mock.calls.filter(call => call[0] === NOTICE.empty);
      expect(keptCalls.length).toBeGreaterThan(0);
      for (const call of keptCalls) expect(call).toHaveLength(1);
    });

    it('shows only the latest notice of a row and links only that field to it', async () => {
      const user = userEvent.setup();
      renderWithFilters([STORED.LowPass]);

      const frequency = await screen.findByRole<HTMLInputElement>('spinbutton', {
        name: NAME.cutoff,
      });
      const q = screen.getByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(frequency);
      await user.type(frequency, '99999');
      await user.tab();
      expect(noticeOf(frequency)).not.toBeNull();

      await user.clear(q);
      await user.tab();
      expect(shownNotices()).toHaveLength(1);
      expect(frequency).not.toHaveAttribute('aria-describedby');
      expect(noticeOf(q)?.textContent.trim()).toBe(NOTICE.empty);

      // Editing the field that does not own the notice leaves it in place
      await user.type(frequency, '1');
      expect(noticeOf(q)?.textContent.trim()).toBe(NOTICE.empty);
    });

    it('clears a notice on the next edit of the field', async () => {
      const user = userEvent.setup();
      renderWithFilters([STORED.LowPass]);

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, Q_ABOVE_MAX);
      await user.tab();
      expect(t).toHaveBeenCalledWith(NOTICE.max, { value: Q_MAX });
      expect(shownNotices()).toHaveLength(1);
      const noticeId = q.getAttribute('aria-describedby');

      await user.type(q, '1');
      expect(q).not.toHaveAttribute('aria-describedby');
      expect(document.getElementById(noticeId ?? '')).toHaveTextContent('');
    });

    it('clears a notice in the new-filter row when the filter type changes', async () => {
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowPass');

      const frequency = await screen.findByRole<HTMLInputElement>('spinbutton', {
        name: NAME.cutoff,
      });
      await user.clear(frequency);
      await user.type(frequency, '99999');
      await user.tab();
      expect(shownNotices()).toHaveLength(1);
      expect(frequency).toHaveAttribute('aria-describedby');

      await pickType(user, 'HighPass');
      const after = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.cutoff });
      expect(after.value).toBe('100');
      expect(after).not.toHaveAttribute('aria-describedby');
      expect(shownNotices()).toEqual([]);
    });

    it('clears a notice when the row above is removed', async () => {
      const user = userEvent.setup();
      const filters: EqualizerFilters = [
        { type: 'LowPass', frequency: 12000, q: 0.5, passes: 1 },
        { type: 'LowPass', frequency: 15000, q: 2, passes: 1 },
      ];
      const { rerender } = renderWithFilters(filters);

      const [first] = await screen.findAllByRole<HTMLInputElement>('spinbutton', {
        name: NAME.q,
      });
      await user.clear(first);
      await user.type(first, Q_ABOVE_MAX);
      await user.tab();
      expect(shownNotices()).toHaveLength(1);

      // The parent applies the removal of the first row; the second row moves up
      await rerender({ equalizerSettings: { enabled: true, filters: [filters[1]] } });

      const [q] = screen.getAllByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      expect(q.value).toBe('2');
      expect(q).not.toHaveAttribute('aria-describedby');
      expect(shownNotices()).toEqual([]);
    });

    it('clears a notice in the new-filter row when the new type has the same default', async () => {
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowPass');

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.tab();
      expect(q.value).toBe(String(DEFAULT_EQ_Q));
      expect(shownNotices()).toHaveLength(1);

      // HighPass also defaults Q to DEFAULT_EQ_Q, so the value does not change
      await pickType(user, 'HighPass');
      const after = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      expect(after.value).toBe(String(DEFAULT_EQ_Q));
      expect(after).not.toHaveAttribute('aria-describedby');
      expect(shownNotices()).toEqual([]);
    });

    it('clears a notice when the row above is removed and the next filter has the same value', async () => {
      const user = userEvent.setup();
      const filters: EqualizerFilters = [
        { type: 'LowPass', frequency: 12000, q: DEFAULT_EQ_Q, passes: 1 },
        { type: 'LowPass', frequency: 15000, q: DEFAULT_EQ_Q, passes: 1 },
      ];
      const { rerender } = renderWithFilters(filters);

      const [first] = await screen.findAllByRole<HTMLInputElement>('spinbutton', {
        name: NAME.q,
      });
      await user.clear(first);
      await user.tab();
      expect(shownNotices()).toHaveLength(1);

      // The parent applies the removal of the first row; a filter of the same type and Q moves up
      await rerender({ equalizerSettings: { enabled: true, filters: [filters[1]] } });

      const [q] = screen.getAllByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      expect(q.value).toBe(String(DEFAULT_EQ_Q));
      expect(q).not.toHaveAttribute('aria-describedby');
      expect(shownNotices()).toEqual([]);
    });

    it('clears a notice when an outside update returns the value to an earlier commit', async () => {
      const user = userEvent.setup();
      const withQ = (q: number) => ({
        equalizerSettings: {
          enabled: true,
          filters: [{ type: 'LowPass' as const, frequency: 15000, q, passes: 1 }],
        },
      });
      const { rerender } = renderWithFilters(withQ(2).equalizerSettings.filters);

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, '5');
      await user.tab();
      // The parent stores the commit, then another update changes it to 8
      await rerender(withQ(5));
      await rerender(withQ(8));

      await user.clear(q);
      await user.tab();
      expect(shownNotices()).toHaveLength(1);

      // An outside update brings back 5, the value this input committed earlier
      await rerender(withQ(5));
      expect(q.value).toBe('5');
      expect(q).not.toHaveAttribute('aria-describedby');
      expect(shownNotices()).toEqual([]);
    });

    it('disables every parameter input and the attenuation dropdown when disabled', async () => {
      render(AudioEqualizerSettings, {
        props: {
          equalizerSettings: { enabled: true, filters: [STORED.LowPass, STORED.BandReject] },
          disabled: true,
          onUpdate: mockUpdateCallback,
        },
      });

      const inputs = await screen.findAllByRole('spinbutton');
      expect(inputs).toHaveLength(4);
      for (const input of inputs) expect(input).toBeDisabled();
      for (const trigger of screen.getAllByRole('combobox', { name: NAME.attenuation })) {
        expect(trigger).toBeDisabled();
      }
    });

    it('commits and clamps a gain for a served type that has one', async () => {
      // A config that serves a shelf type with a gain, as the backend table defines one
      const lowShelf = {
        LowShelf: {
          Parameters: [
            {
              Name: 'Frequency',
              Label: 'Transition Frequency',
              Type: 'number',
              Unit: 'Hz',
              Min: 20,
              Max: 20000,
              Default: 100,
            },
            {
              Name: 'Gain',
              Label: 'Gain',
              Type: 'number',
              Unit: 'dB',
              Min: -30,
              Max: 30,
              Default: 0,
            },
            {
              Name: 'Passes',
              Label: 'Attenuation',
              Type: 'number',
              Unit: '',
              Min: 0,
              Max: 4,
              Default: 0,
            },
          ],
        },
      };
      global.fetch = vi.fn(() =>
        Promise.resolve({ ok: true, json: () => Promise.resolve(lowShelf) } as unknown as Response)
      );
      const user = userEvent.setup();
      renderEmpty();
      await pickType(user, 'LowShelf');

      const gain = await screen.findByRole<HTMLInputElement>('spinbutton', {
        name: `${KEY}gain (dB)`,
      });
      expect(gain).toHaveAttribute('step', '0.1');
      await user.clear(gain);
      await user.type(gain, '1.26');
      await user.tab();
      expect(gain.value).toBe('1.3');
      expect(gain.validity.valid).toBe(true);

      await user.clear(gain);
      await user.type(gain, '50');
      await user.tab();
      expect(gain.value).toBe('30');
      expect(noticeOf(gain)?.textContent.trim()).not.toBe('');

      await user.click(screen.getByText('settings.audio.audioFilters.addFilter'));
      expect(lastPayload().filters[0]).toMatchObject({ type: 'LowShelf', gain: 30 });
    });

    it('keeps a notice when the editor becomes disabled', async () => {
      const user = userEvent.setup();
      const { rerender } = renderWithFilters([STORED.LowPass]);

      const q = await screen.findByRole<HTMLInputElement>('spinbutton', { name: NAME.q });
      await user.clear(q);
      await user.type(q, Q_ABOVE_MAX);
      await user.tab();
      expect(shownNotices()).toHaveLength(1);

      await rerender({ disabled: true });

      expect(q).toBeDisabled();
      expect(shownNotices()).toHaveLength(1);
      expect(noticeOf(q)).not.toBeNull();
    });

    it('names the frequency, Q and bandwidth inputs of existing and new filters', async () => {
      const user = userEvent.setup();
      renderWithFilters([
        { type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 1 },
        { type: 'BandReject' as const, frequency: 1000, width: 100, q: 0.707, passes: 1 },
      ]);

      await screen.findAllByRole('spinbutton', { name: NAME.q });
      const count = (name: string) => screen.queryAllByRole('spinbutton', { name }).length;
      expect([count(NAME.cutoff), count(NAME.center)]).toEqual([1, 1]);
      expect([count(NAME.q), count(NAME.width)]).toEqual([1, 1]);

      await pickType(user, 'HighPass');
      expect([count(NAME.cutoff), count(NAME.q)]).toEqual([2, 2]);

      await pickType(user, 'BandReject');
      expect([count(NAME.center), count(NAME.width)]).toEqual([2, 2]);
    });

    describe('Accessibility', () => {
      it('labels each parameter input and the attenuation dropdown with a label element', async () => {
        const user = userEvent.setup();
        renderWithFilters([STORED.LowPass, STORED.BandReject]);
        await pickType(user, 'LowPass');

        const labelled = [
          ...screen.getAllByLabelText(NAME.cutoff),
          ...screen.getAllByLabelText(NAME.q),
          ...screen.getAllByLabelText(NAME.center),
          ...screen.getAllByLabelText(NAME.width),
          ...screen.getAllByLabelText(NAME.attenuation),
        ];
        // Edit rows: LowPass 3 and BandReject 3 controls; new LowPass row: 3 controls
        expect(labelled).toHaveLength(9);

        const labels = Array.from(document.querySelectorAll('label[for]')).filter(
          l => l.textContent.includes(KEY) && !l.textContent.includes(`${KEY}newFilterType`)
        );
        expect(labels).toHaveLength(9);
        for (const label of labels) {
          const control = document.getElementById(label.getAttribute('for') ?? '');
          expect(control).not.toBeNull();
          expect(control).not.toHaveAttribute('aria-label');
          await user.click(label);
          expect(control).toHaveFocus();
        }

        const ids = labels.map(l => l.getAttribute('for'));
        expect(new Set(ids).size).toBe(ids.length);
      });

      it('gives every control a unique id when several editors share a page', async () => {
        const editors = [STORED.LowPass, STORED.BandReject].map(filter =>
          render(AudioEqualizerSettings, {
            props: {
              equalizerSettings: { enabled: true, filters: [filter] },
              onUpdate: mockUpdateCallback,
            },
          })
        );
        await waitFor(() => {
          expect(screen.getAllByRole('combobox', { name: NAME.attenuation })).toHaveLength(2);
        });

        for (const { container } of editors) {
          const labels = Array.from(container.querySelectorAll('label[for]'));
          // Frequency, Q or Width, and Attenuation of the one filter row
          expect(labels.length).toBeGreaterThanOrEqual(3);
          for (const label of labels) {
            const id = label.getAttribute('for') ?? '';
            expect(document.querySelectorAll(`[id="${id}"]`)).toHaveLength(1);
            expect(container.contains(document.getElementById(id))).toBe(true);
          }
        }
      });
    });
  });
});
