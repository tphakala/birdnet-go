import { describe, it, expect, vi } from 'vitest';
import { tick } from 'svelte';
import { fireEvent, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { Mic, Video } from '@lucide/svelte';
import { createComponentTestFactory } from '../../../../test/render-helpers';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';
import Harness from './RadioCardGroup.test.svelte';
import type { RadioCardOption } from './RadioCardGroup.types';

const VALUES = ['a', 'b', 'c', 'd'];

/** Builds `count` options named a, b, c, d; the indexes in `disabled` are disabled with a reason. */
function makeOptions(count: number, disabled: number[] = []): RadioCardOption[] {
  return VALUES.slice(0, count).map((value, index): RadioCardOption => {
    const label = `Option ${value}`;
    return disabled.includes(index)
      ? { value, label, disabled: true, disabledReason: `Reason ${value}` }
      : { value, label };
  });
}

interface RenderOptions {
  options: RadioCardOption[];
  initial?: string | null;
  columns?: 1 | 2;
  className?: string;
  groupAttrs?: Record<string, string>;
  dropPrevious?: boolean;
}

function renderGroup({ initial = null, ...rest }: RenderOptions) {
  const spy = vi.fn<(value: string) => void>();
  const user = userEvent.setup();
  const result = createComponentTestFactory(Harness).render({ initial, spy, ...rest });
  return { spy, user, ...result };
}

const radios = () => screen.getAllByRole('radio');
const checkedValues = () =>
  radios()
    .filter(r => r.getAttribute('aria-checked') === 'true')
    .map(r => r.textContent.trim());
const tabStops = () => radios().filter(r => r.getAttribute('tabindex') === '0');
const before = () => screen.getByText('before');
const after = () => screen.getByText('after');

/** Puts focus on `before` and presses Tab, which enters the group. */
async function tabIntoGroup(user: ReturnType<typeof userEvent.setup>) {
  before().focus();
  await user.tab();
}

describe('RadioCardGroup', () => {
  it('renders one radio per option in a radiogroup and checks the option equal to value', () => {
    renderGroup({ options: makeOptions(3), initial: 'b' });

    expect(screen.getByRole('radiogroup', { name: 'Test group' })).toBeInTheDocument();
    expect(radios()).toHaveLength(3);
    expect(radios()[1]).toHaveAttribute('aria-checked', 'true');
    expect(radios()[0]).toHaveAttribute('aria-checked', 'false');
    expect(radios()[2]).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('radio', { name: 'Option b' })).toBe(radios()[1]);
  });

  it.each([null, 'zzz'])('checks nothing when value is %s', initial => {
    renderGroup({ options: makeOptions(3), initial });

    expect(checkedValues()).toEqual([]);
  });

  it('makes the checked option the only Tab stop', () => {
    renderGroup({ options: makeOptions(3), initial: 'c' });

    expect(tabStops()).toEqual([radios()[2]]);
    expect(radios()[0]).toHaveAttribute('tabindex', '-1');
    expect(radios()[1]).toHaveAttribute('tabindex', '-1');
  });

  it('makes the first enabled option the only Tab stop when nothing is checked', () => {
    renderGroup({ options: makeOptions(3, [0]), initial: null });

    expect(tabStops()).toEqual([radios()[1]]);
  });

  it('keeps a checked disabled option as the Tab stop', () => {
    renderGroup({ options: makeOptions(3, [2]), initial: 'c' });

    expect(tabStops()).toEqual([radios()[2]]);
  });

  it('makes the first option the Tab stop when every option is disabled and none is checked', () => {
    renderGroup({ options: makeOptions(3, [0, 1, 2]), initial: null });

    expect(tabStops()).toEqual([radios()[0]]);
  });

  it('Tab enters on the checked option and the next Tab leaves the group', async () => {
    const { user } = renderGroup({ options: makeOptions(3), initial: 'b' });

    await tabIntoGroup(user);
    expect(document.activeElement).toBe(radios()[1]);

    await user.tab();
    expect(document.activeElement).toBe(after());

    await user.tab({ shift: true });
    expect(document.activeElement).toBe(radios()[1]);
  });

  it('Tab into a group with nothing checked focuses the first enabled option without checking it', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(3, [0]), initial: null });

    await tabIntoGroup(user);

    expect(document.activeElement).toBe(radios()[1]);
    expect(spy).not.toHaveBeenCalled();
    expect(checkedValues()).toEqual([]);
  });

  it.each([
    { count: 3, from: 0, key: 'ArrowDown', to: 1 },
    { count: 3, from: 0, key: 'ArrowRight', to: 1 },
    { count: 3, from: 1, key: 'ArrowUp', to: 0 },
    { count: 3, from: 1, key: 'ArrowLeft', to: 0 },
    { count: 3, from: 2, key: 'ArrowDown', to: 0 },
    { count: 3, from: 2, key: 'ArrowRight', to: 0 },
    { count: 3, from: 0, key: 'ArrowUp', to: 2 },
    { count: 3, from: 0, key: 'ArrowLeft', to: 2 },
    { count: 2, from: 0, key: 'ArrowDown', to: 1 },
    { count: 2, from: 1, key: 'ArrowDown', to: 0 },
    { count: 2, from: 0, key: 'ArrowLeft', to: 1 },
    { count: 2, from: 1, key: 'ArrowUp', to: 0 },
  ])('$key from $from moves focus and selection to $to ($count options)', async spec => {
    const options = makeOptions(spec.count);
    const { user, spy } = renderGroup({ options, initial: options[spec.from].value });
    await tabIntoGroup(user);

    await user.keyboard(`{${spec.key}}`);

    await waitFor(() => expect(document.activeElement).toBe(radios()[spec.to]));
    expect(radios()[spec.to]).toHaveAttribute('aria-checked', 'true');
    expect(radios()[spec.to]).toHaveAttribute('tabindex', '0');
    expect(spy).toHaveBeenLastCalledWith(options[spec.to].value);
    expect(checkedValues()).toHaveLength(1);
  });

  it('skips disabled options and wraps past them', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(4, [1, 3]), initial: 'a' });
    await tabIntoGroup(user);

    await user.keyboard('{ArrowDown}');
    await waitFor(() => expect(document.activeElement).toBe(radios()[2]));
    expect(spy).toHaveBeenLastCalledWith('c');

    await user.keyboard('{ArrowDown}');
    await waitFor(() => expect(document.activeElement).toBe(radios()[0]));
    expect(spy).toHaveBeenLastCalledWith('a');

    await user.keyboard('{ArrowUp}');
    await waitFor(() => expect(document.activeElement).toBe(radios()[2]));
    expect(spy).toHaveBeenLastCalledWith('c');
  });

  it('does nothing on an arrow key when no other option is enabled', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(3, [1, 2]), initial: 'a' });
    await tabIntoGroup(user);

    await user.keyboard('{ArrowDown}{ArrowUp}{ArrowRight}{ArrowLeft}');
    await tick();

    expect(spy).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(radios()[0]);
  });

  it('does nothing on an arrow key in a one-option group', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(1), initial: null });
    await tabIntoGroup(user);

    await user.keyboard('{ArrowDown}');
    await tick();

    expect(spy).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(radios()[0]);
  });

  it('checks the focused option on Space', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(3, [0]), initial: null });
    await tabIntoGroup(user);

    await user.keyboard(' ');

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith('b');
    expect(radios()[1]).toHaveAttribute('aria-checked', 'true');
  });

  it('checks the focused option on Enter, like a click', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(3), initial: null });
    await tabIntoGroup(user);

    await user.keyboard('{Enter}');

    expect(spy).toHaveBeenCalledTimes(1);
    expect(spy).toHaveBeenCalledWith('a');
  });

  it('ignores click and Space on a disabled option', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(3, [1]), initial: 'a' });

    await user.click(radios()[1]);
    radios()[1].focus();
    await user.keyboard(' ');

    expect(spy).not.toHaveBeenCalled();
    expect(checkedValues()).toEqual(['Option a']);
  });

  it('calls onChange again when the checked option is clicked', async () => {
    const { user, spy } = renderGroup({ options: makeOptions(2), initial: 'a' });

    await user.click(radios()[0]);
    await user.click(radios()[0]);

    expect(spy).toHaveBeenCalledTimes(2);
    expect(spy).toHaveBeenLastCalledWith('a');
  });

  it.each(['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'])(
    'prevents the default action of %s when it moves the selection',
    async key => {
      renderGroup({ options: makeOptions(3), initial: 'b' });
      radios()[1].focus();

      const notCancelled = await fireEvent.keyDown(radios()[1], { key });

      expect(notCancelled).toBe(false);
    }
  );

  it.each([
    { key: 'Home' },
    { key: 'End' },
    { key: 'PageDown' },
    { key: 'Escape' },
    { key: 'a' },
    { key: 'Tab' },
    { key: 'ArrowDown', altKey: true },
    { key: 'ArrowRight', ctrlKey: true },
    { key: 'ArrowLeft', metaKey: true },
  ])('leaves $key alone', async init => {
    const { spy } = renderGroup({ options: makeOptions(3), initial: 'b' });
    radios()[1].focus();

    const notCancelled = await fireEvent.keyDown(radios()[1], init);
    await tick();

    expect(notCancelled).toBe(true);
    expect(spy).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(radios()[1]);
  });

  describe('Tab stop invariant', () => {
    const KEY_SEQUENCES = [
      ['ArrowDown'],
      ['ArrowUp'],
      ['ArrowDown', 'ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'],
      ['ArrowLeft', 'ArrowLeft', 'ArrowLeft', 'ArrowLeft', 'ArrowLeft'],
      ['ArrowRight', 'ArrowRight', 'ArrowRight', 'ArrowRight', 'ArrowUp'],
    ];

    /** The option index the arrow key moves to, or the same index when nothing else is enabled. */
    function nextIndex(options: RadioCardOption[], from: number, key: string): number {
      const step = key === 'ArrowDown' || key === 'ArrowRight' ? 1 : -1;
      for (let offset = 1; offset < options.length; offset++) {
        const candidate = (from + step * offset + options.length * offset) % options.length;
        if (!options[candidate].disabled) return candidate;
      }
      return from;
    }

    /** First enabled option, or the first option when all are disabled. */
    const firstEnabled = (options: RadioCardOption[]) =>
      Math.max(
        0,
        options.findIndex(o => !o.disabled)
      );

    async function expectTabStop(index: number) {
      await waitFor(() => expect(document.activeElement).toBe(radios()[index]));
      expect(tabStops()).toEqual([radios()[index]]);
    }

    const cases = [1, 2, 3, 4].flatMap(count =>
      [null, 'a'].flatMap(initial =>
        [undefined, 0, 1, count - 1].flatMap(disabledIndex =>
          // Disabling the only option of a group is not a case worth a row
          disabledIndex !== undefined && count === 1
            ? []
            : [
                {
                  count,
                  initial,
                  disabledIndex,
                  name: `${count} options, initial ${initial}, disabled ${disabledIndex}`,
                },
              ]
        )
      )
    );

    it.each(cases)('keeps exactly one Tab stop through key sequences ($name)', async spec => {
      const options = makeOptions(
        spec.count,
        spec.disabledIndex === undefined ? [] : [spec.disabledIndex]
      );
      // The model starts from an enabled option; a checked disabled one is its own test above
      const initial = options.find(o => o.value === spec.initial && !o.disabled)?.value ?? null;

      for (const keys of KEY_SEQUENCES) {
        const { user, spy, unmount } = renderGroup({ options, initial });
        await tabIntoGroup(user);

        let index = initial === null ? firstEnabled(options) : VALUES.indexOf(initial);
        // What the parent holds: nothing until an option is checked
        let checkedLabel: string[] = initial === null ? [] : [`Option ${initial}`];
        let lastValue: string | undefined;
        await expectTabStop(index);

        for (const key of keys) {
          const target = nextIndex(options, index, key);
          await user.keyboard(`{${key}}`);
          if (target !== index) {
            checkedLabel = [`Option ${VALUES[target]}`];
            lastValue = VALUES[target];
          }
          index = target;

          await expectTabStop(index);
          expect(checkedValues()).toEqual(checkedLabel);
          expect(spy.mock.lastCall?.[0]).toBe(lastValue);
        }
        unmount();
      }
    });

    it('matches a model over a seeded random walk of 200 arrow keys', async () => {
      // mulberry32: a tiny deterministic PRNG, so a failure repeats
      let state = 0x9e3779b9;
      const random = () => {
        state = (state + 0x6d2b79f5) | 0;
        let t = Math.imul(state ^ (state >>> 15), 1 | state);
        t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
        return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
      };
      const arrows = ['ArrowDown', 'ArrowUp', 'ArrowLeft', 'ArrowRight'];
      const options = makeOptions(4, [2]);
      const { user, spy } = renderGroup({ options, initial: 'a' });
      await tabIntoGroup(user);

      let index = 0;
      for (let step = 0; step < 200; step++) {
        const key = arrows[Math.floor(random() * arrows.length)] ?? 'ArrowDown';
        index = nextIndex(options, index, key);
        await user.keyboard(`{${key}}`);

        await expectTabStop(index);
        expect(radios()[index]).toHaveAttribute('aria-checked', 'true');
        expect(checkedValues()).toHaveLength(1);
        expect(spy).toHaveBeenLastCalledWith(options[index].value);
      }
    });
  });

  it('passes extra attributes to the radiogroup and merges className', () => {
    renderGroup({
      options: makeOptions(2),
      className: 'custom-class',
      groupAttrs: { 'aria-describedby': 'help-text', 'data-testid': 'the-group' },
    });

    const group = screen.getByTestId('the-group');
    expect(group).toBe(screen.getByRole('radiogroup'));
    expect(group).toHaveAttribute('aria-describedby', 'help-text');
    expect(group).toHaveClass('custom-class', 'grid');
  });

  it('colours the checked option icon with the primary colour and the others with base content', () => {
    const options: RadioCardOption[] = [
      { value: 'a', label: 'Option a', icon: Mic },
      { value: 'b', label: 'Option b', icon: Video },
    ];
    renderGroup({ options, initial: 'a' });

    const checkedIcon = radios()[0].querySelector('svg');
    const otherIcon = radios()[1].querySelector('svg');
    expect(checkedIcon).toHaveClass('text-[var(--color-primary)]');
    expect(checkedIcon).not.toHaveClass('text-[var(--color-base-content)]');
    expect(otherIcon).toHaveClass('text-[var(--color-base-content)]');
    expect(otherIcon).not.toHaveClass('text-[var(--color-primary)]');
  });

  it('renders the badge with base content text, not the primary colour', () => {
    const options: RadioCardOption[] = [
      { value: 'a', label: 'Option a', badge: 'Recommended' },
      { value: 'b', label: 'Option b' },
    ];
    renderGroup({ options, initial: 'a' });

    const badge = screen.getByText('Recommended');
    expect(badge).toHaveClass('text-[var(--color-base-content)]');
    expect(badge).not.toHaveClass('text-[var(--color-primary)]');
    expect(radios()[0]).toHaveAccessibleName(/Option a\s*Recommended/);
  });

  it('renders the description and the detail inside the option', () => {
    const options: RadioCardOption[] = [
      { value: 'a', label: 'Option a', description: 'About a', detail: 'Detail a' },
    ];
    renderGroup({ options, initial: null });

    const radio = radios()[0];
    expect(within(radio).getByText('About a')).toBeInTheDocument();
    expect(within(radio).getByText('Detail a')).toHaveClass('font-mono');
  });

  it('shows the disabled reason inside the disabled option', () => {
    renderGroup({ options: makeOptions(2, [1]), initial: 'a' });

    const disabled = radios()[1];
    expect(disabled).toHaveAttribute('aria-disabled', 'true');
    expect(disabled).toHaveClass('cursor-not-allowed');
    expect(within(disabled).getByText('Reason b')).toBeInTheDocument();
    expect(radios()[0]).not.toHaveAttribute('aria-disabled');
  });

  it('lays out two columns when columns is 2', () => {
    const { unmount } = renderGroup({ options: makeOptions(2), columns: 2 });
    expect(screen.getByRole('radiogroup')).toHaveClass('grid-cols-2');
    unmount();

    renderGroup({ options: makeOptions(2) });
    expect(screen.getByRole('radiogroup')).not.toHaveClass('grid-cols-2');
  });

  it('moves focus correctly after an option is removed', async () => {
    const { user, rerender } = renderGroup({ options: makeOptions(3), initial: 'a' });
    await tabIntoGroup(user);
    const withoutMiddle = makeOptions(3).filter(o => o.value !== 'b');

    await rerender({ options: withoutMiddle });
    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(document.activeElement).toBe(radios()[1]));
    expect(radios()).toHaveLength(2);
    expect(radios()[1]).toHaveTextContent('Option c');
    expect(checkedValues()).toEqual(['Option c']);
  });
});

describe('RadioCardGroup option list changes', () => {
  it('focuses the card that was chosen when the change also removes an earlier card', async () => {
    const { user, spy } = renderGroup({
      options: makeOptions(3),
      initial: 'a',
      dropPrevious: true,
    });
    await tabIntoGroup(user);

    await user.keyboard('{ArrowDown}');

    await waitFor(() => expect(radios()).toHaveLength(2));
    expect(spy).toHaveBeenCalledWith('b');
    expect(checkedValues()).toEqual(['Option b']);
    expect(document.activeElement).toBe(radios()[0]);
    expect(radios()[0]).toHaveTextContent('Option b');
  });

  it('aligns icons to the top when a disabled option shows its reason', () => {
    renderGroup({ options: makeOptions(2, [1]), initial: 'a' });

    expect(radios()[0]).toHaveClass('items-start');
    expect(radios()[1]).toHaveClass('items-start');
  });

  it('centres icons when no option has a second line', () => {
    renderGroup({ options: makeOptions(2), initial: 'a' });

    expect(radios()[0]).toHaveClass('items-center');
  });
});

describe('RadioCardGroup Accessibility', () => {
  it.each([
    { name: 'with nothing checked', options: makeOptions(3), initial: null, columns: 1 as const },
    { name: 'with one option checked', options: makeOptions(3), initial: 'b', columns: 1 as const },
    {
      name: 'with a disabled option',
      options: makeOptions(3, [1]),
      initial: 'a',
      columns: 1 as const,
    },
    { name: 'in two columns', options: makeOptions(2), initial: 'a', columns: 2 as const },
  ])('has no violations $name', async spec => {
    const { container } = renderGroup({
      options: spec.options,
      initial: spec.initial,
      columns: spec.columns,
    });

    await expect(expectNoA11yViolations(container)).resolves.toBeUndefined();
  });
});
