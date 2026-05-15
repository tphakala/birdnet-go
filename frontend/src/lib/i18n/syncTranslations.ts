#!/usr/bin/env tsx

/* eslint-disable no-console, no-undef */

import { readFileSync, writeFileSync } from 'fs';
import { join } from 'path';
import { LOCALE_CODES, DEFAULT_LOCALE } from './config.js';

interface SyncResult {
  locale: string;
  addedKeys: string[];
  removedKeys: string[];
  modified: boolean;
}

interface SyncOptions {
  check: boolean;
  verbose: boolean;
}

type JsonObject = Record<string, unknown>;

class TranslationSync {
  private readonly messagesPath = join(process.cwd(), 'static/messages');

  sync(options: SyncOptions): boolean {
    const reference = this.loadJson(DEFAULT_LOCALE);
    if (!reference) {
      console.error(`Failed to load reference locale ${DEFAULT_LOCALE}.json`);
      process.exit(1);
    }

    const results: SyncResult[] = [];
    let anyModified = false;

    for (const locale of LOCALE_CODES) {
      if (locale === DEFAULT_LOCALE) continue;

      const result = this.syncLocale(locale, reference, options);
      results.push(result);
      if (result.modified) anyModified = true;
    }

    this.printSummary(results, options);
    return anyModified;
  }

  private syncLocale(locale: string, reference: JsonObject, options: SyncOptions): SyncResult {
    const existing = this.loadJson(locale) ?? {};
    const addedKeys: string[] = [];
    const removedKeys: string[] = [];

    const merged = this.deepMerge(reference, existing, '', addedKeys, removedKeys);
    const modified = addedKeys.length > 0 || removedKeys.length > 0;

    if (modified && !options.check) {
      this.writeJson(locale, merged);
    }

    return { locale, addedKeys, removedKeys, modified };
  }

  private deepMerge(
    reference: JsonObject,
    existing: JsonObject,
    prefix: string,
    addedKeys: string[],
    removedKeys: string[]
  ): JsonObject {
    const result: JsonObject = {};

    for (const key of Object.keys(reference)) {
      const fullKey = prefix ? `${prefix}.${key}` : key;
      const refValue = reference[key]; // eslint-disable-line security/detect-object-injection
      const existingValue = existing[key]; // eslint-disable-line security/detect-object-injection

      if (typeof refValue === 'object' && refValue !== null && !Array.isArray(refValue)) {
        const existingObj =
          typeof existingValue === 'object' &&
          existingValue !== null &&
          !Array.isArray(existingValue)
            ? (existingValue as JsonObject)
            : {};
        // eslint-disable-next-line security/detect-object-injection
        result[key] = this.deepMerge(
          refValue as JsonObject,
          existingObj,
          fullKey,
          addedKeys,
          removedKeys
        );
      } else {
        if (existingValue === undefined || typeof existingValue !== typeof refValue) {
          result[key] = refValue; // eslint-disable-line security/detect-object-injection
          addedKeys.push(fullKey);
        } else {
          result[key] = existingValue; // eslint-disable-line security/detect-object-injection
        }
      }
    }

    this.collectOrphans(existing, reference, prefix, removedKeys);

    return result;
  }

  private collectOrphans(
    existing: JsonObject,
    reference: JsonObject,
    prefix: string,
    removedKeys: string[]
  ): void {
    for (const key of Object.keys(existing)) {
      if (key in reference) continue;
      const fullKey = prefix ? `${prefix}.${key}` : key;
      const value = existing[key]; // eslint-disable-line security/detect-object-injection
      if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
        const leafKeys = this.getAllLeafKeys(value as JsonObject, fullKey);
        removedKeys.push(...leafKeys);
      } else {
        removedKeys.push(fullKey);
      }
    }
  }

  private getAllLeafKeys(obj: JsonObject, prefix: string): string[] {
    const keys: string[] = [];
    for (const [key, value] of Object.entries(obj)) {
      const fullKey = `${prefix}.${key}`;
      if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
        keys.push(...this.getAllLeafKeys(value as JsonObject, fullKey));
      } else {
        keys.push(fullKey);
      }
    }
    return keys;
  }

  private loadJson(locale: string): JsonObject | null {
    const filePath = join(this.messagesPath, `${locale}.json`);
    try {
      // eslint-disable-next-line security/detect-non-literal-fs-filename
      return JSON.parse(readFileSync(filePath, 'utf-8')) as JsonObject;
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ENOENT') {
        console.error(`File not found: ${filePath}`);
      } else {
        console.error(`Failed to load ${locale}.json:`, error);
      }
      return null;
    }
  }

  private writeJson(locale: string, data: JsonObject): void {
    const filePath = join(this.messagesPath, `${locale}.json`);
    try {
      // eslint-disable-next-line security/detect-non-literal-fs-filename
      writeFileSync(filePath, JSON.stringify(data, null, 2) + '\n', 'utf-8');
    } catch (error) {
      console.error(`Failed to write ${locale}.json:`, error);
      process.exit(1);
    }
  }

  private printSummary(results: SyncResult[], options: SyncOptions): void {
    const modifiedCount = results.filter(r => r.modified).length;
    const totalAdded = results.reduce((sum, r) => sum + r.addedKeys.length, 0);
    const totalRemoved = results.reduce((sum, r) => sum + r.removedKeys.length, 0);

    if (modifiedCount === 0) {
      console.log('All locale files are in sync with en.json.');
      return;
    }

    const mode = options.check ? '[dry-run] ' : '';
    console.log(
      `${mode}${modifiedCount} locale(s) out of sync: +${totalAdded} added, -${totalRemoved} removed\n`
    );

    for (const result of results) {
      if (!result.modified) continue;

      console.log(
        `  ${result.locale.toUpperCase()}: +${result.addedKeys.length} added, -${result.removedKeys.length} removed`
      );

      if (options.verbose) {
        for (const key of result.addedKeys) {
          console.log(`    + ${key}`);
        }
        for (const key of result.removedKeys) {
          console.log(`    - ${key}`);
        }
      }
    }

    if (options.check) {
      console.log('\nRun without --check to apply changes.');
    }
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);

  if (args.includes('--help') || args.includes('-h')) {
    console.log(`
i18n Sync - Synchronize locale files with en.json key structure

Usage: npm run i18n:sync -- [options]

Options:
  --check     Dry-run mode: report what would change without writing files
  --verbose   List every added/removed key
  --help, -h  Show this help message

In default mode, missing keys are filled with the English value as fallback,
and orphaned keys (not in en.json) are removed. Existing translations are
preserved. Key ordering matches en.json to reduce diff noise.
`);
    process.exit(0);
  }

  const options: SyncOptions = {
    check: args.includes('--check'),
    verbose: args.includes('--verbose') || args.includes('-v'),
  };

  const syncer = new TranslationSync();
  const modified = syncer.sync(options);

  if (options.check && modified) {
    process.exit(1);
  }
}

export { TranslationSync };
export type { SyncOptions, SyncResult };
