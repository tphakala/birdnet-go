/**
 * Button behaviour and accessibility tests (axe-core)
 */
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { expectNoA11yViolations, getA11yReport, A11Y_CONFIGS } from '$lib/utils/axe-utils';
import ButtonHarness from './Button.test.svelte';

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
    expect(button).toHaveClass('aria-disabled:opacity-50', 'aria-disabled:cursor-not-allowed');
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

  it('fades a blocked button less while it has keyboard focus so its ring stays visible', () => {
    render(ButtonHarness, { props: { 'aria-disabled': 'true' } });

    expect(screen.getByRole('button')).toHaveClass('aria-disabled:focus-visible:opacity-75');
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
