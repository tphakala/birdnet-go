/**
 * Accessibility test example demonstrating axe-core integration
 * Tests basic HTML button accessibility
 */
import { describe, it, expect } from 'vitest';
import { expectNoA11yViolations, getA11yReport, A11Y_CONFIGS } from '$lib/utils/axe-utils';

describe('Button Accessibility Tests', () => {
  it('should have no accessibility violations with proper label', async () => {
    // Create a button element directly in JSDOM
    document.body.innerHTML = '<button>Click Me</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();

    // Test with strict accessibility rules
    if (button) {
      await expectNoA11yViolations(button, A11Y_CONFIGS.strict);
    }

    // Cleanup
    document.body.innerHTML = '';
  });

  it('should fail accessibility test without proper label', async () => {
    // Create button without accessible name
    document.body.innerHTML = '<button></button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();
    expect(button).not.toBeNull();
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

    const report = button ? await getA11yReport(button, A11Y_CONFIGS.lenient) : '';

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

    if (button) {
      await expectNoA11yViolations(button, A11Y_CONFIGS.forms);
    }

    // Cleanup
    document.body.innerHTML = '';
  });

  it('should handle disabled state accessibly', async () => {
    // Create disabled button
    document.body.innerHTML = '<button disabled>Disabled Button</button>';
    const button = document.querySelector('button');
    expect(button).toBeTruthy();

    if (button) {
      await expectNoA11yViolations(button, A11Y_CONFIGS.strict);
    }

    // Cleanup
    document.body.innerHTML = '';
  });
});
