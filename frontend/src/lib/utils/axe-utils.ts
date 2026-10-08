/**
 * Accessibility testing utilities using axe-core
 */
import type { AxeResults, RunOptions } from 'axe-core';

// axe-core is only imported in browser environment
let axeCore: typeof import('axe-core') | null = null;

/**
 * Initialize axe-core (browser only)
 */
async function initAxe() {
  if (typeof window === 'undefined') {
    throw new Error('axe-core can only be used in browser environment');
  }

  axeCore ??= await import('axe-core');

  return axeCore;
}

/**
 * Run accessibility tests on a DOM element
 * @param element - Element to test (defaults to document)
 * @param options - axe-core run options
 * @returns Promise with accessibility test results
 */
export async function runAxeTest(
  element: Element | Document = document,
  options: RunOptions = {}
): Promise<AxeResults> {
  const axe = await initAxe();

  // Copy so axe never mutates the caller's options
  const runOptions: RunOptions = { ...options };

  return axe.run(element, runOptions);
}

/**
 * Assert that element has no accessibility violations
 * Throws error with detailed violation info if any found
 * @param element - Element to test
 * @param options - axe-core run options
 */
export async function expectNoA11yViolations(
  element: Element | Document = document,
  options: RunOptions = {}
): Promise<void> {
  const results = await runAxeTest(element, options);

  if (results.violations.length > 0) {
    const violationSummary = results.violations
      .map(violation => {
        const targets = violation.nodes.map(node => node.target.join(' ')).join(', ');
        return `- ${violation.id}: ${violation.description}\n  Impact: ${violation.impact}\n  Targets: ${targets}`;
      })
      .join('\n\n');

    throw new Error(
      `Found ${results.violations.length} accessibility violation(s):\n\n${violationSummary}`
    );
  }
}

/**
 * Get accessibility test results as formatted report
 * @param element - Element to test
 * @param options - axe-core run options
 * @returns Formatted accessibility report
 */
export async function getA11yReport(
  element: Element | Document = document,
  options: RunOptions = {}
): Promise<string> {
  const results = await runAxeTest(element, options);

  const report = [
    `🔍 Accessibility Test Results`,
    `━━━━━━━━━━━━━━━━━━━━━━━━━━━━`,
    `✅ Rules Passed: ${results.passes.length}`,
    `⚠️  Violations: ${results.violations.length}`,
    `ℹ️  Incomplete: ${results.incomplete.length}`,
    `🚫 Inapplicable: ${results.inapplicable.length}`,
    '',
  ];

  if (results.violations.length > 0) {
    report.push('🚨 VIOLATIONS:');
    results.violations.forEach(violation => {
      report.push(`\n${violation.id} (${violation.impact})`);
      report.push(`Description: ${violation.description}`);
      report.push(`Help: ${violation.helpUrl}`);
      violation.nodes.forEach(node => {
        report.push(`  Target: ${node.target.join(' ')}`);
        if (node.html) {
          report.push(
            `  HTML: ${node.html.substring(0, 100)}${node.html.length > 100 ? '...' : ''}`
          );
        }
      });
    });
  }

  if (results.incomplete.length > 0) {
    report.push('\n⚠️  INCOMPLETE CHECKS:');
    results.incomplete.forEach(item => {
      report.push(`- ${item.id}: ${item.description}`);
    });
  }

  return report.join('\n');
}

/**
 * Default configuration for common accessibility tests. Only `rules` is set:
 * axe-core ignores a `tags` key, and switching to `runOnly` would change which
 * rules run.
 */
export const A11Y_CONFIGS: Readonly<Record<'strict' | 'lenient' | 'forms', RunOptions>> = {
  // Strict configuration for critical accessibility
  strict: {
    rules: {
      'color-contrast': { enabled: true },
      'aria-valid-attr': { enabled: true },
      label: { enabled: true },
      'button-name': { enabled: true },
      'link-name': { enabled: true },
      'heading-order': { enabled: true },
      'landmark-unique': { enabled: true },
    },
  },

  // Lenient configuration for development
  lenient: {
    rules: {
      'color-contrast': { enabled: false }, // Often fails in dev
      'aria-valid-attr': { enabled: true },
      label: { enabled: true },
    },
  },

  // Form-specific accessibility tests
  forms: {
    rules: {
      label: { enabled: true },
      'aria-valid-attr': { enabled: true },
      'aria-required-attr': { enabled: true },
      'form-field-multiple-labels': { enabled: true },
    },
  },
};
