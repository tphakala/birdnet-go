#!/usr/bin/env tsx
/**
 * Translation file validator for BirdNET-Go i18n
 *
 * Validates translation files for:
 * - Completeness (missing/extra keys)
 * - Correctness (valid JSON, ICU syntax, parameters)
 * - Quality (untranslated, empty values)
 *
 * Usage:
 *   npm run i18n:validate
 *   npm run i18n:validate -- --strict
 *   npm run i18n:validate -- --min-coverage 90
 */

/* eslint-disable no-console, no-undef */

import { readFileSync } from 'fs';
import { join } from 'path';
import { parse as parseICU } from '@formatjs/icu-messageformat-parser';
import { LOCALE_CODES, DEFAULT_LOCALE } from './config.js';

interface ValidationResult {
  locale: string;
  totalKeys: number;
  missingKeys: string[];
  extraKeys: string[];
  emptyValues: string[];
  untranslated: string[];
  invalidICU: Array<{ key: string; error: string }>;
  parameterMismatches: Array<{ key: string; expected: string[]; actual: string[] }>;
  errors: string[];
  warnings: string[];
}

interface ValidationOptions {
  strictMode?: boolean;
  allowUntranslated?: boolean;
  minCoverage?: number; // Percentage (0-100)
  failOnWarnings?: boolean;
}

class TranslationValidator {
  private readonly messagesPath = join(process.cwd(), 'static/messages');
  private referenceMessages: Record<string, unknown> = {};
  private readonly results: ValidationResult[] = [];

  async validate(options: ValidationOptions = {}): Promise<boolean> {
    console.log('🌍 Validating translation files...\n');

    // Load reference (English)
    this.referenceMessages = this.loadMessages(DEFAULT_LOCALE);
    const referenceKeys = this.getAllKeys(this.referenceMessages);

    console.log(`📚 Reference (${DEFAULT_LOCALE}.json): ${referenceKeys.length} keys\n`);

    // Validate each locale
    for (const locale of LOCALE_CODES) {
      if (locale === DEFAULT_LOCALE) continue;

      const result = await this.validateLocale(locale, referenceKeys, options);
      this.results.push(result);
    }

    // Print results
    this.printResults(options);

    // Return overall pass/fail
    return this.checkThresholds(options);
  }

  private loadMessages(locale: string): Record<string, unknown> {
    const filePath = join(this.messagesPath, `${locale}.json`);
    try {
      // eslint-disable-next-line security/detect-non-literal-fs-filename
      return JSON.parse(readFileSync(filePath, 'utf-8')) as Record<string, unknown>;
    } catch (error) {
      // In strict/CI mode, we want to fail fast on missing files
      // In development, we return empty object to allow partial validation
      console.error(`❌ Failed to load ${locale}.json:`, error);
      console.error(`   File path: ${filePath}`);

      if (error instanceof Error) {
        if (error.message.includes('ENOENT')) {
          console.error(
            `   → File does not exist. Run 'npm run i18n:validate' from frontend directory.`
          );
        } else if (error.message.includes('JSON')) {
          console.error(`   → Invalid JSON syntax. Please fix the file.`);
        }
      }

      return {};
    }
  }

  private getAllKeys(obj: Record<string, unknown>, prefix = ''): string[] {
    const keys: string[] = [];

    for (const [key, value] of Object.entries(obj)) {
      const fullKey = prefix ? `${prefix}.${key}` : key;

      if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
        keys.push(...this.getAllKeys(value as Record<string, unknown>, fullKey));
      } else {
        keys.push(fullKey);
      }
    }

    return keys;
  }

  private getValueByPath(obj: Record<string, unknown>, path: string): unknown {
    return path.split('.').reduce((current, key) => {
      // eslint-disable-next-line security/detect-object-injection
      return current && typeof current === 'object'
        ? (current as Record<string, unknown>)[key]
        : undefined;
    }, obj as unknown);
  }

  private async validateLocale(
    locale: string,
    referenceKeys: string[],
    options: ValidationOptions
  ): Promise<ValidationResult> {
    const result: ValidationResult = {
      locale,
      totalKeys: 0,
      missingKeys: [],
      extraKeys: [],
      emptyValues: [],
      untranslated: [],
      invalidICU: [],
      parameterMismatches: [],
      errors: [],
      warnings: [],
    };

    const messages = this.loadMessages(locale);
    const messageKeys = this.getAllKeys(messages);
    result.totalKeys = messageKeys.length;

    // Find missing keys
    result.missingKeys = referenceKeys.filter(key => !messageKeys.includes(key));

    // Find extra keys
    result.extraKeys = messageKeys.filter(key => !referenceKeys.includes(key));

    // Check each key
    for (const key of referenceKeys) {
      const value = this.getValueByPath(messages, key);
      const referenceValue = this.getValueByPath(this.referenceMessages, key);

      // Skip if missing (already tracked)
      if (value === undefined) continue;

      // Check for empty values
      if (typeof value === 'string' && value.trim() === '') {
        result.emptyValues.push(key);
      }

      // Check for untranslated (same as English)
      if (!options.allowUntranslated && value === referenceValue) {
        result.untranslated.push(key);
      }

      // Validate ICU syntax
      if (typeof value === 'string' && typeof referenceValue === 'string') {
        this.validateICUSyntax(key, value, result);
        this.validateParameters(key, referenceValue, value, result);
      }
    }

    return result;
  }

  private validateICUSyntax(key: string, value: string, result: ValidationResult): void {
    // Skip ICU validation for placeholder keys that contain literal template syntax examples
    // These keys (e.g., titlePlaceholder, messagePlaceholder) show users Go template syntax
    // like {{.CommonName}} which is not ICU MessageFormat and should not be validated
    if (key.endsWith('Placeholder')) return;

    // Check if message contains ICU syntax
    if (!value.includes('{')) return;

    try {
      parseICU(value);
    } catch (error) {
      result.invalidICU.push({
        key,
        error: error instanceof Error ? error.message : String(error),
      });
    }
  }

  private validateParameters(
    key: string,
    reference: string,
    translation: string,
    result: ValidationResult
  ): void {
    const refParams = this.extractParameters(reference);
    const transParams = this.extractParameters(translation);

    // Check if all reference parameters exist in translation
    const missing = refParams.filter(p => !transParams.includes(p));
    const extra = transParams.filter(p => !refParams.includes(p));

    if (missing.length > 0 || extra.length > 0) {
      result.parameterMismatches.push({
        key,
        expected: refParams,
        actual: transParams,
      });
    }
  }

  private extractParameters(text: string): string[] {
    const params = new Set<string>();

    // Use ICU parser to properly extract parameters from AST
    // This avoids false positives from words inside literal text
    try {
      const ast = parseICU(text);
      this.extractParamsFromAST(ast, params);
    } catch {
      // If parsing fails, fall back to simple regex for non-ICU messages
      // This regex only matches simple {param} patterns without any commas
      const simpleParamRegex = /\{(\w+)\}/g;
      let match;
      while ((match = simpleParamRegex.exec(text)) !== null) {
        params.add(match[1]);
      }
    }

    return Array.from(params).sort();
  }

  private extractParamsFromAST(
    elements: ReturnType<typeof parseICU>,
    params: Set<string>
  ): void {
    for (const element of elements) {
      // Handle different AST node types based on type field
      const node = element as unknown as Record<string, unknown>;

      // Type 1 = argument (actual ICU parameter like {name})
      if ('type' in node && node.type === 1 && 'value' in node && typeof node.value === 'string') {
        params.add(node.value);
      }

      // Type 6 = plural/select node (like {count, plural, ...})
      if ('type' in node && node.type === 6 && 'value' in node && typeof node.value === 'string') {
        // Add the parameter name (e.g., "count" from {count, plural, ...})
        params.add(node.value);
      }

      // Recursively process nested options in plural/select nodes
      if ('options' in node && typeof node.options === 'object' && node.options !== null) {
        const options = node.options as Record<string, unknown>;
        for (const option of Object.values(options)) {
          if (option && typeof option === 'object' && 'value' in option) {
            const optionObj = option as Record<string, unknown>;
            if (Array.isArray(optionObj.value)) {
              this.extractParamsFromAST(optionObj.value as ReturnType<typeof parseICU>, params);
            }
          }
        }
      }
    }
  }

  private printResults(options: ValidationOptions): void {
    console.log('\n╔══════════════════════════════════════════════════════════╗');
    console.log('║         Translation Validation Results                  ║');
    console.log('╚══════════════════════════════════════════════════════════╝\n');

    for (const result of this.results) {
      const coverage = (
        (result.totalKeys / this.getAllKeys(this.referenceMessages).length) *
        100
      ).toFixed(2);
      const status = this.getStatus(result, options);

      console.log(
        `${status} ${result.locale.toUpperCase()}: ${result.totalKeys} keys (${coverage}% coverage)`
      );

      if (result.missingKeys.length > 0) {
        console.log(`  ⚠️  Missing: ${result.missingKeys.length} keys`);
        if (options.strictMode) {
          console.log(
            `      ${result.missingKeys.slice(0, 5).join(', ')}${result.missingKeys.length > 5 ? '...' : ''}`
          );
        }
      }

      if (result.extraKeys.length > 0) {
        console.log(`  ℹ️  Extra: ${result.extraKeys.length} keys (outdated?)`);
      }

      if (result.emptyValues.length > 0) {
        console.log(`  ❌ Empty values: ${result.emptyValues.length}`);
      }

      if (result.untranslated.length > 0 && !options.allowUntranslated) {
        console.log(`  ⚠️  Untranslated: ${result.untranslated.length}`);
      }

      if (result.invalidICU.length > 0) {
        console.log(`  ❌ Invalid ICU syntax: ${result.invalidICU.length}`);
        result.invalidICU.forEach(({ key, error }) => {
          console.log(`      ${key}: ${error}`);
        });
      }

      if (result.parameterMismatches.length > 0) {
        console.log(`  ❌ Parameter mismatches: ${result.parameterMismatches.length}`);
        if (options.strictMode) {
          result.parameterMismatches.forEach(({ key, expected, actual }) => {
            console.log(
              `      ${key}: expected [${expected.join(', ')}], got [${actual.join(', ')}]`
            );
          });
        }
      }

      console.log('');
    }
  }

  private getStatus(result: ValidationResult, options: ValidationOptions): string {
    const hasErrors =
      result.emptyValues.length > 0 ||
      result.invalidICU.length > 0 ||
      result.parameterMismatches.length > 0;

    const coverage = (result.totalKeys / this.getAllKeys(this.referenceMessages).length) * 100;
    const belowThreshold = options.minCoverage && coverage < options.minCoverage;

    if (hasErrors || belowThreshold) return '❌';
    if (result.missingKeys.length > 0 || result.untranslated.length > 0) return '⚠️ ';
    return '✅';
  }

  private checkThresholds(options: ValidationOptions): boolean {
    let passed = true;

    for (const result of this.results) {
      // Check for critical errors
      if (
        result.emptyValues.length > 0 ||
        result.invalidICU.length > 0 ||
        result.parameterMismatches.length > 0
      ) {
        passed = false;
      }

      // Check coverage threshold
      if (options.minCoverage) {
        const coverage = (result.totalKeys / this.getAllKeys(this.referenceMessages).length) * 100;
        if (coverage < options.minCoverage) {
          console.log(
            `❌ ${result.locale}: Coverage ${coverage.toFixed(2)}% below threshold ${options.minCoverage}%`
          );
          passed = false;
        }
      }

      // Check for warnings in strict mode
      if (
        options.failOnWarnings &&
        (result.missingKeys.length > 0 || result.untranslated.length > 0)
      ) {
        passed = false;
      }
    }

    return passed;
  }

  generateReport(format: 'json' | 'markdown' = 'json'): string {
    if (format === 'json') {
      return JSON.stringify(this.results, null, 2);
    } else {
      return this.generateMarkdownReport();
    }
  }

  private generateMarkdownReport(): string {
    const lines = ['# Translation Validation Report\n'];
    const refKeyCount = this.getAllKeys(this.referenceMessages).length;

    lines.push(`**Reference:** ${refKeyCount} keys in ${DEFAULT_LOCALE}.json\n`);
    lines.push('## Summary\n');
    lines.push('| Locale | Keys | Coverage | Missing | Extra | Issues |');
    lines.push('|--------|------|----------|---------|-------|--------|');

    for (const result of this.results) {
      const coverage = ((result.totalKeys / refKeyCount) * 100).toFixed(2);
      const issues =
        result.emptyValues.length + result.invalidICU.length + result.parameterMismatches.length;
      lines.push(
        `| ${result.locale} | ${result.totalKeys} | ${coverage}% | ${result.missingKeys.length} | ${result.extraKeys.length} | ${issues} |`
      );
    }

    lines.push('\n## Detailed Results\n');

    for (const result of this.results) {
      lines.push(`### ${result.locale.toUpperCase()}\n`);

      if (result.missingKeys.length > 0) {
        lines.push(`**Missing Keys (${result.missingKeys.length}):**\n`);
        lines.push('```');
        lines.push(result.missingKeys.join('\n'));
        lines.push('```\n');
      }

      if (result.invalidICU.length > 0) {
        lines.push(`**Invalid ICU Syntax (${result.invalidICU.length}):**\n`);
        result.invalidICU.forEach(({ key, error }) => {
          lines.push(`- \`${key}\`: ${error}`);
        });
        lines.push('');
      }

      if (result.parameterMismatches.length > 0) {
        lines.push(`**Parameter Mismatches (${result.parameterMismatches.length}):**\n`);
        result.parameterMismatches.forEach(({ key, expected, actual }) => {
          lines.push(`- \`${key}\`: expected [${expected.join(', ')}], got [${actual.join(', ')}]`);
        });
        lines.push('');
      }
    }

    return lines.join('\n');
  }

  getResults(): ValidationResult[] {
    return this.results;
  }

  getReferenceKeys(): string[] {
    return this.getAllKeys(this.referenceMessages);
  }
}

// CLI execution
if (import.meta.url === `file://${process.argv[1]}`) {
  const validator = new TranslationValidator();

  // Parse CLI options
  const args = process.argv.slice(2);
  const jsonOutput = args.includes('--json');
  const options: ValidationOptions = {
    strictMode: args.includes('--strict'),
    allowUntranslated: args.includes('--allow-untranslated'),
    failOnWarnings: args.includes('--fail-on-warnings'),
    minCoverage: args.includes('--min-coverage')
      ? parseFloat(args[args.indexOf('--min-coverage') + 1])
      : undefined,
  };

  // Suppress console output if JSON output requested
  if (jsonOutput) {
    const originalLog = console.log;
    console.log = () => {};
    const passed = await validator.validate(options);
    console.log = originalLog;

    // Output LLM-friendly structured JSON
    const results = validator.getResults();
    const referenceKeys = validator.getReferenceKeys();
    const jsonReport = {
      success: passed,
      timestamp: new Date().toISOString(),
      summary: {
        totalLocales: results.length,
        referenceKeyCount: referenceKeys.length,
        passedLocales: results.filter(
          r =>
            r.missingKeys.length === 0 &&
            r.emptyValues.length === 0 &&
            r.invalidICU.length === 0 &&
            r.parameterMismatches.length === 0
        ).length,
        totalErrors: results.reduce(
          (sum, r) =>
            sum + r.emptyValues.length + r.invalidICU.length + r.parameterMismatches.length,
          0
        ),
        totalWarnings: results.reduce(
          (sum, r) => sum + r.missingKeys.length + r.untranslated.length,
          0
        ),
      },
      errors: results.flatMap(r => [
        ...r.emptyValues.map(key => ({
          type: 'empty_value',
          locale: r.locale,
          key,
          severity: 'error',
          message: `Translation key "${key}" has empty value in ${r.locale}.json`,
          file: `static/messages/${r.locale}.json`,
          fixable: true,
          suggestedFix: `Add translation for key "${key}"`,
        })),
        ...r.invalidICU.map(({ key, error }) => ({
          type: 'invalid_icu',
          locale: r.locale,
          key,
          error,
          severity: 'error',
          message: `Invalid ICU MessageFormat syntax in "${key}": ${error}`,
          file: `static/messages/${r.locale}.json`,
          fixable: true,
          suggestedFix: `Fix ICU syntax error: ${error}`,
        })),
        ...r.parameterMismatches.map(({ key, expected, actual }) => ({
          type: 'parameter_mismatch',
          locale: r.locale,
          key,
          expected,
          actual,
          missing: expected.filter(p => !actual.includes(p)),
          extra: actual.filter(p => !expected.includes(p)),
          severity: 'error',
          message: `Parameter mismatch in "${key}"`,
          file: `static/messages/${r.locale}.json`,
          fixable: true,
          suggestedFix: `Update parameters to match: {${expected.join('}, {')}}`,
        })),
      ]),
      warnings: results.flatMap(r => [
        ...r.missingKeys.map(key => ({
          type: 'missing_key',
          locale: r.locale,
          key,
          severity: 'warning',
          message: `Missing translation key "${key}"`,
          file: `static/messages/${r.locale}.json`,
          referenceFile: `static/messages/${DEFAULT_LOCALE}.json`,
          fixable: true,
          suggestedFix: `Copy key from ${DEFAULT_LOCALE}.json and translate`,
        })),
        ...r.untranslated.map(key => ({
          type: 'untranslated',
          locale: r.locale,
          key,
          severity: 'warning',
          message: `Translation identical to English`,
          file: `static/messages/${r.locale}.json`,
          fixable: true,
          suggestedFix: `Translate to ${r.locale}`,
        })),
        ...r.extraKeys.map(key => ({
          type: 'extra_key',
          locale: r.locale,
          key,
          severity: 'info',
          message: `Extra key not in ${DEFAULT_LOCALE}.json`,
          file: `static/messages/${r.locale}.json`,
          fixable: true,
          suggestedFix: `Remove key or add to ${DEFAULT_LOCALE}.json`,
        })),
      ]),
      locales: results.map(r => ({
        locale: r.locale,
        totalKeys: r.totalKeys,
        coverage: Number(((r.totalKeys / referenceKeys.length) * 100).toFixed(2)),
        errors: r.emptyValues.length + r.invalidICU.length + r.parameterMismatches.length,
        warnings: r.missingKeys.length + r.untranslated.length,
        info: r.extraKeys.length,
      })),
    };

    console.log(JSON.stringify(jsonReport, null, 2));
    process.exit(passed ? 0 : 1);
  }

  // Suppress console output if generating report
  const generateReport = args.includes('--report');
  if (generateReport) {
    const originalLog = console.log;
    const originalError = console.error;
    console.log = () => {};
    console.error = () => {};

    const passed = await validator.validate(options);

    console.log = originalLog;
    console.error = originalError;

    const format = args.includes('--format=markdown') ? 'markdown' : 'json';
    const report = validator.generateReport(format);
    console.log(report);

    process.exit(passed ? 0 : 1);
  }

  const passed = await validator.validate(options);
  process.exit(passed ? 0 : 1);
}

export { TranslationValidator };
export type { ValidationOptions, ValidationResult };
