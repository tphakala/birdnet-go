/**
 * Button behaviour and accessibility tests (axe-core)
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { expectNoA11yViolations, getA11yReport, A11Y_CONFIGS } from '$lib/utils/axe-utils';
import ButtonHarness from './Button.test.svelte';
import { opacityClasses } from '../../../../test/class-helpers';

const VARIANTS = ['default', 'primary', 'success', 'warning', 'error', 'ghost'] as const;

describe('Button', () => {
  it('binds ref to the rendered button element', async () => {
    const holder: { el?: HTMLButtonElement | null } = {};
    render(ButtonHarness, { holder });

    await vi.waitFor(() => expect(holder.el).toBeDefined());
    expect(holder.el).toBeInstanceOf(HTMLButtonElement);
    expect(holder.el).toBe(screen.getByRole('button'));
  });

  it('keeps an aria-disabled button focusable and clickable, styled as disabled', async () => {
    const onclick = vi.fn();
    render(ButtonHarness, { 'aria-disabled': 'true', onclick });
    const button = screen.getByRole('button');

    button.focus();
    await userEvent.setup().click(button);

    expect(button).toHaveAttribute('aria-disabled', 'true');
    expect(button).not.toBeDisabled();
    expect(button).toHaveFocus();
    expect(onclick).toHaveBeenCalledTimes(1);
    expect(button).toHaveClass('cursor-not-allowed');
    // Element opacity would fade the focus ring, so the dimming is in the colours
    expect(opacityClasses(button, { ignoreVariant: 'disabled' })).toEqual([]);
    expect(button.className).not.toContain('aria-disabled:pointer-events-none');
  });

  it('draws the shared focus-visible outline', () => {
    render(ButtonHarness);

    expect(screen.getByRole('button')).toHaveClass(
      'focus-visible:outline-2',
      'focus-visible:outline-[var(--color-primary)]',
      'focus-visible:outline-offset-2'
    );
  });

  it('passes aria-pressed and aria-describedby through to the button', () => {
    render(ButtonHarness, { 'aria-pressed': true, 'aria-describedby': 'reason' });
    const button = screen.getByRole('button');

    expect(button).toHaveAttribute('aria-pressed', 'true');
    expect(button).toHaveAttribute('aria-describedby', 'reason');
  });
  it('merges a plain class attribute with its own classes', () => {
    render(ButtonHarness, { props: { class: 'extra-class' } });

    const button = screen.getByRole('button');
    expect(button).toHaveClass('extra-class');
    expect(button).toHaveClass('inline-flex');
  });
  it('darkens the primary variant on hover and press instead of fading it', () => {
    render(ButtonHarness, { props: { variant: 'primary' } });

    const button = screen.getByRole('button');
    expect(button).toHaveClass(
      'hover:bg-[var(--color-primary-hover)]',
      'active:bg-[var(--color-primary-hover)]'
    );
    expect(button.className).not.toContain('bg-[var(--color-primary)]/');
  });

  it.each(VARIANTS)(
    'dims a blocked %s button through its colours, with the ring at full strength and no press feedback',
    variant => {
      render(ButtonHarness, { props: { variant, 'aria-disabled': 'true' } });
      const button = screen.getByRole('button');

      expect(button).toHaveClass(
        'focus-visible:outline-2',
        'focus-visible:outline-[var(--color-primary)]',
        'focus-visible:outline-offset-2'
      );
      expect(opacityClasses(button, { ignoreVariant: 'disabled' })).toEqual([]);
      expect(button.className).not.toMatch(/(^|\s)(hover|active):/);
      // The label is dimmed by its colour's alpha instead of element opacity. Primary keeps its
      // light label over the half-strength fill, as 50% opacity used to draw it.
      const label =
        variant === 'primary'
          ? /(^|\s)text-\[var\(--color-primary-content\)\](\s|$)/
          : /(^|\s)text-\[var\(--[a-z0-9-]+\)\]\/50(\s|$)/;
      expect(button.className).toMatch(label);
    }
  );

  it.each(VARIANTS)('gives a blocked %s button none of its enabled colours', variant => {
    const colours = (el: Element | null) =>
      [...(el?.classList ?? [])].filter(c => /^(bg|text|border)-\[/.test(c));
    const enabled = render(ButtonHarness, { props: { variant } }).container.querySelector('button');
    const blocked = render(ButtonHarness, {
      props: { variant, 'aria-disabled': 'true' },
    }).container.querySelector('button');

    // Primary's label colour is the one class it keeps (see the dimming test above)
    const kept = variant === 'primary' ? ['text-[var(--color-primary-content)]'] : [];

    expect(colours(blocked).length).toBeGreaterThan(0);
    expect(colours(blocked).filter(c => colours(enabled).includes(c))).toEqual(kept);
  });

  it('treats a boolean aria-disabled like the string form', () => {
    render(ButtonHarness, { props: { variant: 'primary', 'aria-disabled': true } });
    const button = screen.getByRole('button');

    expect(button).toHaveClass('cursor-not-allowed');
    expect(button).not.toHaveClass('bg-[var(--color-primary)]');
  });

  it('keeps the plain variant look under native disabled even with aria-disabled set', () => {
    render(ButtonHarness, {
      props: { variant: 'primary', disabled: true, 'aria-disabled': 'true' },
    });
    const button = screen.getByRole('button');

    expect(button).toHaveClass('bg-[var(--color-primary)]', 'disabled:opacity-50');
    expect(button).not.toHaveClass('cursor-not-allowed');
  });

  it('follows aria-disabled changes: the solid fill and hover style return when it is cleared', async () => {
    const { rerender } = render(ButtonHarness, {
      props: { variant: 'primary', 'aria-disabled': 'true' },
    });
    const button = screen.getByRole('button');
    expect(button).not.toHaveClass('bg-[var(--color-primary)]');

    for (const cleared of [undefined, 'false'] as const) {
      await rerender({ variant: 'primary', 'aria-disabled': 'true' });
      expect(button).not.toHaveClass('hover:bg-[var(--color-primary-hover)]');

      await rerender({ variant: 'primary', 'aria-disabled': cleared });
      expect(button).toHaveClass(
        'bg-[var(--color-primary)]',
        'hover:bg-[var(--color-primary-hover)]'
      );
    }
  });

  it('does not animate the focus ring colour', () => {
    render(ButtonHarness);

    const button = screen.getByRole('button');
    expect(button).not.toHaveClass('transition-colors');
    expect(button).toHaveClass('motion-reduce:transition-none');
  });
});

describe('Button Accessibility Tests', () => {
  it('should have no accessibility violations with proper label', async () => {
    render(ButtonHarness);
    const button = screen.getByRole('button');
    expect(button).toHaveAccessibleName('Press me');

    await expectNoA11yViolations(button, A11Y_CONFIGS.strict);
  });

  it('axe helper rejects a raw button without an accessible name', async () => {
    // Create button without accessible name
    document.body.innerHTML = '<button></button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    if (!button) throw new Error('Button not found');

    // This should throw due to missing button label
    await expect(expectNoA11yViolations(button, A11Y_CONFIGS.strict)).rejects.toThrow(
      /button-name/
    );

    // Cleanup
    document.body.innerHTML = '';
  });

  it('axe helper builds a report for a raw button', async () => {
    // Create accessible button
    document.body.innerHTML = '<button>Save Changes</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    if (!button) throw new Error('Button not found');

    const report = await getA11yReport(button, A11Y_CONFIGS.lenient);

    expect(report).toContain('Accessibility Test Results');
    expect(report).toContain('Rules Passed:');
    expect(report).toContain('Violations:');

    // Cleanup
    document.body.innerHTML = '';
  });

  it('should pass form accessibility rules for submit button', async () => {
    render(ButtonHarness, { props: { type: 'submit' } });
    const button = screen.getByRole('button');
    expect(button).toHaveAttribute('type', 'submit');

    await expectNoA11yViolations(button, A11Y_CONFIGS.forms);
  });

  it('should handle disabled state accessibly', async () => {
    render(ButtonHarness, { props: { disabled: true } });
    const button = screen.getByRole('button');
    expect(button).toBeDisabled();

    await expectNoA11yViolations(button, A11Y_CONFIGS.strict);
  });
});
