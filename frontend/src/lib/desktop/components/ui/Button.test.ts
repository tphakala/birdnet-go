/**
 * Accessibility test example demonstrating axe-core integration
 * Tests basic HTML button accessibility
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
});

describe('Button Accessibility Tests', () => {
  it('should have no accessibility violations with proper label', async () => {
    // Create a button element directly in JSDOM
    document.body.innerHTML = '<button>Click Me</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    if (!button) throw new Error('Button not found');

    // Test with strict accessibility rules
    await expectNoA11yViolations(button, A11Y_CONFIGS.strict);

    // Cleanup
    document.body.innerHTML = '';
  });

  it('should fail accessibility test without proper label', async () => {
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

  it('should generate accessibility report', async () => {
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
    // Create submit button
    document.body.innerHTML = '<button type="submit">Submit Form</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    if (!button) throw new Error('Button not found');

    await expectNoA11yViolations(button, A11Y_CONFIGS.forms);

    // Cleanup
    document.body.innerHTML = '';
  });

  it('should handle disabled state accessibly', async () => {
    // Create disabled button
    document.body.innerHTML = '<button disabled>Disabled Button</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    if (!button) throw new Error('Button not found');

    await expectNoA11yViolations(button, A11Y_CONFIGS.strict);

    // Cleanup
    document.body.innerHTML = '';
  });
});
