import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'svelte';
import EqualizerNumberInput from './EqualizerNumberInput.svelte';
import {
  EQ_GAIN_INPUT_STEP,
  EQ_HZ_INPUT_STEP,
  DEFAULT_EQ_GAIN_DB,
} from '$lib/utils/audio/equalizer';

// Gain range of the shelf and peaking types in the backend filter table
// (internal/conf/eqfilter_config.go); no served type has a gain yet
const GAIN_MIN_DB = -30;
const GAIN_MAX_DB = 30;

type Props = ComponentProps<typeof EqualizerNumberInput>;

function gainProps(overrides: Partial<Props> = {}): Props {
  return {
    label: 'Gain',
    unit: 'dB',
    value: DEFAULT_EQ_GAIN_DB,
    min: GAIN_MIN_DB,
    max: GAIN_MAX_DB,
    step: EQ_GAIN_INPUT_STEP,
    variant: 'edit',
    onCommit: vi.fn(),
    onNotice: vi.fn(),
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe('EqualizerNumberInput', () => {
  it.each([
    { typed: '1.26', shown: '1.3', committed: 1.3, notice: null },
    { typed: '-12.5', shown: '-12.5', committed: -12.5, notice: null },
    {
      typed: '50',
      shown: '30',
      committed: GAIN_MAX_DB,
      notice: { kind: 'max', value: GAIN_MAX_DB },
    },
    {
      typed: '-50',
      shown: '-30',
      committed: GAIN_MIN_DB,
      notice: { kind: 'min', value: GAIN_MIN_DB },
    },
  ])(
    'commits a gain of $typed as $committed on change',
    async ({ typed, shown, committed, notice }) => {
      const user = userEvent.setup();
      const props = gainProps();
      render(EqualizerNumberInput, { props });

      const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: 'Gain (dB)' });
      expect(input).toHaveAttribute('step', String(EQ_GAIN_INPUT_STEP));
      await user.clear(input);
      await user.type(input, typed);
      expect(props.onCommit).not.toHaveBeenCalled();
      await user.tab();

      expect(input.value).toBe(shown);
      expect(input.validity.valid).toBe(true);
      expect(props.onCommit).toHaveBeenCalledTimes(1);
      expect(props.onCommit).toHaveBeenCalledWith(committed);
      expect(props.onNotice).toHaveBeenLastCalledWith(notice);
    }
  );

  it('keeps the stored gain and commits nothing on an empty entry', async () => {
    const user = userEvent.setup();
    const props = gainProps({ value: 6 });
    render(EqualizerNumberInput, { props });

    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: 'Gain (dB)' });
    await user.clear(input);
    await user.tab();

    expect(input.value).toBe('6');
    expect(props.onCommit).not.toHaveBeenCalled();
    expect(props.onNotice).toHaveBeenLastCalledWith({ kind: 'empty', value: 6 });
  });

  it('is disabled when disabled, and becoming disabled does not withdraw its notice', async () => {
    const user = userEvent.setup();
    const props = gainProps({ noticeId: undefined });
    const { rerender } = render(EqualizerNumberInput, { props });

    const input = screen.getByRole<HTMLInputElement>('spinbutton', { name: 'Gain (dB)' });
    await user.clear(input);
    await user.type(input, '50');
    await user.tab();
    expect(props.onNotice).toHaveBeenLastCalledWith({ kind: 'max', value: GAIN_MAX_DB });
    // The parent stores the commit and passes it back, as the editor does
    await rerender({ value: GAIN_MAX_DB, noticeId: 'row-notice' });
    vi.mocked(props.onNotice).mockClear();

    await rerender({ disabled: true, noticeId: 'row-notice' });

    expect(input).toBeDisabled();
    expect(input).toHaveAttribute('aria-describedby', 'row-notice');
    expect(props.onNotice).not.toHaveBeenCalled();
  });

  it('gives two instances on one page their own ids, each label naming its own input', () => {
    const containers = ['first', 'second'].map(
      () =>
        render(EqualizerNumberInput, {
          props: gainProps({ label: 'Cutoff Frequency', unit: 'Hz', step: EQ_HZ_INPUT_STEP }),
        }).container
    );

    const ids = containers.map(c => c.querySelector('input')?.id);
    expect(ids[0]).toBeTruthy();
    expect(ids[0]).not.toBe(ids[1]);
    for (const container of containers) {
      const label = container.querySelector('label');
      const input = container.querySelector('input');
      expect(label?.getAttribute('for')).toBe(input?.id);
      expect(document.querySelectorAll(`[id="${input?.id ?? ''}"]`)).toHaveLength(1);
      expect(label?.control).toBe(input);
    }
  });
});
