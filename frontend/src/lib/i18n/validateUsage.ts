#!/usr/bin/env tsx
/**
 * Translation usage validator for BirdNET-Go i18n
 *
 * Scans codebase for t() usage and validates against translation files:
 * - Finds missing translations (keys used in code but not in en.json)
 * - Finds unused translations (keys in en.json never used in code)
 * - Validates parameter consistency
 *
 * Usage:
 *   npm run i18n:scan              # Scan and show all keys
 *   npm run i18n:check-usage       # Check for missing translations
 *   npm run i18n:find-unused       # Find unused translation keys
 */

/* eslint-disable no-console, no-undef */

import { execSync } from 'child_process';
import { readFileSync } from 'fs';
import { join } from 'path';
import { DEFAULT_LOCALE } from './config.js';

// grep -rn 't(' over the whole src/ tree emits far more than Node's 1 MiB
// default execSync buffer. Without this the spawn throws ENOBUFS; that error was
// previously swallowed, so the check silently scanned nothing and still exited 0.
// 64 MiB leaves generous headroom as the codebase grows.
const GREP_MAX_BUFFER_BYTES = 64 * 1024 * 1024;

interface UsageResult {
  usedKeys: Map<string, string[]>; // key -> [file paths]
  missingInTranslations: string[];
  unusedInCode: string[];
  dynamicKeys: Array<{ file: string; line: number }>;
  totalUsages: number;
  totalFiles: number;
}

interface UsageOptions {
  showUnused?: boolean;
  showDetails?: boolean;
  allowDynamic?: boolean;
}

class UsageValidator {
  private readonly messagesPath = join(process.cwd(), 'static/messages');
  private readonly srcPath = join(process.cwd(), 'src');
  private readonly usedKeys = new Map<string, string[]>();
  private translationKeys = new Set<string>();

  async validate(options: UsageOptions = {}): Promise<UsageResult> {
    console.log('🔍 Scanning codebase for translation key usage...\n');

    // Load translation keys from en.json
    this.loadTranslationKeys();

    // Scan codebase using ast-grep
    this.scanCodebase();

    // Analyze results
    const result = this.analyzeUsage(options);

    // Print results
    this.printResults(result, options);

    return result;
  }

  private loadTranslationKeys(): void {
    const filePath = join(this.messagesPath, `${DEFAULT_LOCALE}.json`);
    try {
      // eslint-disable-next-line security/detect-non-literal-fs-filename
      const messages = JSON.parse(readFileSync(filePath, 'utf-8')) as Record<string, unknown>;
      this.translationKeys = new Set(this.getAllKeys(messages));
      console.log(`📚 Loaded ${this.translationKeys.size} keys from ${DEFAULT_LOCALE}.json\n`);
    } catch (error) {
      console.error(`❌ Failed to load ${DEFAULT_LOCALE}.json:`, error);
      process.exit(1);
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

  private scanCodebase(): void {
    try {
      // Use grep instead of ast-grep for scanning
      // Why grep? ast-grep doesn't have native Svelte support yet, and while it can parse
      // TypeScript, Svelte's mixed syntax (HTML + TS in <script>) makes it challenging.
      // grep is:
      // - Simple and reliable for literal string matching
      // - Fast (< 200ms for entire codebase)
      // - Works consistently across .svelte and .ts files
      // - Lower complexity than implementing custom Svelte parser
      //
      // Trade-offs:
      // - Less syntax-aware (can have false positives, which we filter)
      // - Can't validate complex dynamic keys (documented limitation)
      //
      // Security: Command uses process.cwd() which is trusted, no user input
      const output = execSync(`grep -rn 't(' src/ --include="*.svelte" --include="*.ts" || true`, {
        cwd: process.cwd(),
        encoding: 'utf-8',
        maxBuffer: GREP_MAX_BUFFER_BYTES,
      });

      if (!output.trim()) {
        // t() is ubiquitous across the codebase; an empty scan means grep itself
        // failed, not that the code uses no translations. Fail loudly instead.
        throw new Error(
          'i18n usage scan produced no output; the grep scan of src/ is broken. Refusing to report a passing validation on zero scanned files.'
        );
      }

      // Parse grep output: filename:linenum:content
      const lines = output.trim().split('\n');

      for (const line of lines) {
        const match = line.match(/^([^:]+):(\d+):(.+)$/);
        if (!match) continue;

        const [, file, lineNum, content] = match;

        // Extract all t('key') and t("key") calls from the line
        const regex = /t\s*\(\s*['"]([\w.]+)['"]/g;
        let keyMatch;

        while ((keyMatch = regex.exec(content)) !== null) {
          const key = keyMatch[1];

          // Filter out false positives:
          // - Keys must have at least one dot (e.g., "common.save")
          // - Keys must start with a letter
          // - Keys must be longer than 2 characters
          // - Skip test files' test() calls and other false positives
          if (
            !key.includes('.') ||
            key.length < 3 ||
            /^[\d.]+$/.test(key) || // Skip pure numbers like "1" or "10.5"
            /^\./.test(key) || // Skip keys starting with dot
            file.includes('.test.') // Skip test files to reduce noise
          ) {
            continue;
          }

          const files = this.usedKeys.get(key) ?? [];
          const location = `${file}:${lineNum}`;

          if (!files.includes(location)) {
            files.push(location);
            this.usedKeys.set(key, files);
          }
        }
      }

      if (this.usedKeys.size === 0) {
        // Output was non-empty but nothing parsed out as a key: the parser or the
        // false-positive filters are broken. Fail rather than silently pass.
        throw new Error(
          'i18n usage scan matched grep output but extracted zero translation keys; the parser is broken. Refusing to report a passing validation.'
        );
      }

      console.log(
        `   Found ${this.usedKeys.size} unique keys in ${this.countUniqueFiles()} files\n`
      );
    } catch (error) {
      // Never swallow: a failed scan previously reported "0 files" and still exited
      // 0, so i18n:check-usage gave no real coverage. Surface it loudly.
      if (error instanceof Error && error.message.startsWith('i18n usage scan')) {
        throw error;
      }
      throw new Error(
        `i18n usage scan failed: ${error instanceof Error ? error.message : String(error)}`,
        { cause: error }
      );
    }
  }

  private countUniqueFiles(): number {
    const files = new Set<string>();
    for (const locations of this.usedKeys.values()) {
      for (const location of locations) {
        const file = location.split(':')[0];
        files.add(file);
      }
    }
    return files.size;
  }

  private analyzeUsage(options: UsageOptions): UsageResult {
    const missingInTranslations: string[] = [];
    const unusedInCode: string[] = [];

    // Find keys used in code but missing in translations
    for (const key of this.usedKeys.keys()) {
      if (!this.translationKeys.has(key)) {
        missingInTranslations.push(key);
      }
    }

    // Find keys in translations but never used in code
    if (options.showUnused) {
      for (const key of this.translationKeys) {
        if (!this.usedKeys.has(key)) {
          unusedInCode.push(key);
        }
      }
    }

    return {
      usedKeys: this.usedKeys,
      missingInTranslations: missingInTranslations.sort(),
      unusedInCode: unusedInCode.sort(),
      dynamicKeys: [],
      totalUsages: Array.from(this.usedKeys.values()).reduce((sum, locs) => sum + locs.length, 0),
      totalFiles: this.countUniqueFiles(),
    };
  }

  private printResults(result: UsageResult, options: UsageOptions): void {
    console.log('╔══════════════════════════════════════════════════════════╗');
    console.log('║         Translation Usage Validation                    ║');
    console.log('╚══════════════════════════════════════════════════════════╝\n');

    console.log(`📊 Statistics:`);
    console.log(`   Unique translation keys used: ${result.usedKeys.size}`);
    console.log(`   Total t() calls: ${result.totalUsages}`);
    console.log(`   Files scanned: ${result.totalFiles}`);
    console.log(`   Translation keys defined: ${this.translationKeys.size}\n`);

    // Missing translations
    if (result.missingInTranslations.length > 0) {
      console.log(`❌ Missing Translations (${result.missingInTranslations.length} keys)`);
      console.log(`   Keys used in code but not found in ${DEFAULT_LOCALE}.json:\n`);

      for (const key of result.missingInTranslations) {
        const locations = result.usedKeys.get(key) ?? [];
        console.log(`   • ${key}`);
        if (options.showDetails) {
          for (const location of locations) {
            console.log(`     └─ ${location}`);
          }
        } else {
          console.log(
            `     └─ ${locations[0]}${locations.length > 1 ? ` (+${locations.length - 1} more)` : ''}`
          );
        }
      }
      console.log('');
    } else {
      console.log(`✅ All used translation keys exist in ${DEFAULT_LOCALE}.json\n`);
    }

    // Unused translations
    if (options.showUnused && result.unusedInCode.length > 0) {
      console.log(`⚠️  Unused Translations (${result.unusedInCode.length} keys)`);
      console.log(`   Keys defined in ${DEFAULT_LOCALE}.json but never used in code:\n`);

      // Show first 20, then summary
      const displayKeys = result.unusedInCode.slice(0, 20);
      for (const key of displayKeys) {
        console.log(`   • ${key}`);
      }

      if (result.unusedInCode.length > 20) {
        console.log(`   ... and ${result.unusedInCode.length - 20} more unused keys\n`);
      } else {
        console.log('');
      }

      console.log(`   💡 These keys may be:\n`);
      console.log(`      - Dead code that can be removed`);
      console.log(`      - Used dynamically (not detectable by static analysis)`);
      console.log(`      - Used in templates or external files\n`);
    }

    // Summary
    const hasErrors = result.missingInTranslations.length > 0;
    if (hasErrors) {
      console.log('❌ Validation failed: Missing translations detected');
      console.log(
        `   Add these ${result.missingInTranslations.length} keys to ${DEFAULT_LOCALE}.json\n`
      );
    } else {
      console.log('✅ Validation passed: All translation keys validated\n');
    }
  }

  generateReport(format: 'json' | 'markdown', result: UsageResult): string {
    if (format === 'json') {
      return JSON.stringify(
        {
          summary: {
            uniqueKeysUsed: result.usedKeys.size,
            totalUsages: result.totalUsages,
            totalFiles: result.totalFiles,
            translationKeysDefined: this.translationKeys.size,
            missingInTranslations: result.missingInTranslations.length,
            unusedInCode: result.unusedInCode.length,
          },
          missingKeys: result.missingInTranslations,
          unusedKeys: result.unusedInCode,
        },
        null,
        2
      );
    } else {
      const lines = ['# Translation Usage Report\n'];

      lines.push('## Summary\n');
      lines.push(`- **Unique keys used:** ${result.usedKeys.size}`);
      lines.push(`- **Total t() calls:** ${result.totalUsages}`);
      lines.push(`- **Files scanned:** ${result.totalFiles}`);
      lines.push(`- **Keys in ${DEFAULT_LOCALE}.json:** ${this.translationKeys.size}`);
      lines.push(`- **Missing translations:** ${result.missingInTranslations.length}`);
      lines.push(`- **Unused keys:** ${result.unusedInCode.length}\n`);

      if (result.missingInTranslations.length > 0) {
        lines.push(`## ❌ Missing Translations (${result.missingInTranslations.length})\n`);
        lines.push('Keys used in code but not in translation files:\n');
        lines.push('```');
        lines.push(result.missingInTranslations.join('\n'));
        lines.push('```\n');
      }

      if (result.unusedInCode.length > 0) {
        lines.push(`## ⚠️ Unused Translations (${result.unusedInCode.length})\n`);
        lines.push('Keys in translation files but never used in code:\n');
        lines.push('```');
        lines.push(result.unusedInCode.join('\n'));
        lines.push('```\n');
      }

      return lines.join('\n');
    }
  }

  getTranslationKeysCount(): number {
    return this.translationKeys.size;
  }
}

// CLI execution
if (import.meta.url === `file://${process.argv[1]}`) {
  const validator = new UsageValidator();

  // Parse CLI options
  const args = process.argv.slice(2);
  const jsonOutput = args.includes('--json');
  const options: UsageOptions = {
    showUnused: args.includes('--unused') || args.includes('--show-unused'),
    showDetails: args.includes('--details') || args.includes('--verbose'),
    allowDynamic: args.includes('--allow-dynamic'),
  };

  // Suppress console output if JSON output requested
  if (jsonOutput) {
    const originalLog = console.log;
    const originalError = console.error;
    console.log = () => {};
    console.error = () => {};

    const result = await validator.validate(options);

    console.log = originalLog;
    console.error = originalError;

    // Output LLM-friendly structured JSON
    const jsonReport = {
      success: result.missingInTranslations.length === 0,
      timestamp: new Date().toISOString(),
      summary: {
        uniqueKeysUsed: result.usedKeys.size,
        totalUsages: result.totalUsages,
        totalFiles: result.totalFiles,
        translationKeysDefined: validator.getTranslationKeysCount(),
        missingInTranslations: result.missingInTranslations.length,
        unusedInCode: result.unusedInCode.length,
      },
      issues: result.missingInTranslations.map(key => ({
        type: 'missing_translation',
        key,
        severity: 'error',
        message: `Key "${key}" used in code but not in ${DEFAULT_LOCALE}.json`,
        file: DEFAULT_LOCALE + '.json',
        fixable: true,
        suggestedFix: `Add key to ${DEFAULT_LOCALE}.json`,
      })),
      unusedKeys: options.showUnused ? result.unusedInCode : [],
    };

    console.log(JSON.stringify(jsonReport, null, 2));
    process.exit(jsonReport.success ? 0 : 1);
  }

  const result = await validator.validate(options);

  // Generate report if requested
  if (args.includes('--report')) {
    const format = args.includes('--format=markdown') ? 'markdown' : 'json';
    const report = validator.generateReport(format, result);
    console.log('\n' + report);
  }

  // Exit with error if missing translations
  const hasErrors = result.missingInTranslations.length > 0;
  process.exit(hasErrors ? 1 : 0);
}

export { UsageValidator };
export type { UsageOptions, UsageResult };
