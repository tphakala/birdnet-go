/* eslint-disable no-console */
/// <reference types="node" />
/**
 * Script to generate TypeScript types from the English translation file.
 * This ensures compile-time validation of translation keys.
 *
 * Usage:
 *   npx tsx src/lib/i18n/generateTypes.ts            Regenerate types.generated.ts
 *   npx tsx src/lib/i18n/generateTypes.ts --check    Verify the file is in sync (exit 1 on drift)
 *
 * The generated file is committed and verified in CI (i18n-validation.yml); after
 * editing en.json you must regenerate it (npm run generate:i18n-types) and commit
 * the result, or the drift check will fail.
 */

import { readFileSync, writeFileSync } from 'fs';
import { join, dirname } from 'path';
import { fileURLToPath, pathToFileURL } from 'url';
import prettier from 'prettier';
import { parse as parseICU } from '@formatjs/icu-messageformat-parser';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const OUTPUT_PATH = join(__dirname, 'types.generated.ts');
const MESSAGES_PATH = join(__dirname, '../../../static/messages/en.json');

/** Normalizes CRLF to LF so a Windows checkout (core.autocrlf) does not read as drift against Prettier's LF output. */
const normalizeEol = (s: string): string => s.replace(/\r\n/g, '\n');

type TranslationValue = string | Record<string, unknown>;
type TranslationObject = Record<string, TranslationValue>;

interface GeneratedTypes {
  content: string;
  keyCount: number;
  paramCount: number;
}

/**
 * Returns true only for plain nested objects (not null, not arrays). These are
 * the only values the generator recurses into; everything else is a leaf key.
 * Mirrors the guards in syncTranslations.ts and validateTranslations.ts so the
 * three scripts agree on what counts as a key path.
 */
function isNestedObject(value: unknown): value is TranslationObject {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/**
 * Recursively generates TypeScript type definitions from a translation object
 */
function generateTypeFromObject(obj: TranslationObject, prefix = ''): string {
  const lines: string[] = [];

  for (const [key, value] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${key}` : key;

    if (isNestedObject(value)) {
      // Recursively process nested objects
      const nestedTypes = generateTypeFromObject(value, fullKey);
      const nestedLines = nestedTypes.split('\n');
      const filteredLines = nestedLines.filter(l => l.trim());
      lines.push(...filteredLines);
    } else {
      // Leaf key. Parameters can only be extracted from string values.
      const params = typeof value === 'string' ? extractParameters(value) : [];
      if (params.length > 0) {
        lines.push(`  | '${fullKey}' // params: ${params.join(', ')}`);
      } else {
        lines.push(`  | '${fullKey}'`);
      }
    }
  }

  return lines.join('\n');
}

/**
 * Extracts parameter names from a translation string, in order of appearance.
 * e.g., "Hello {name}, you have {count} messages" -> ['name', 'count']
 *
 * Uses the ICU message parser (like validateTranslations.ts) so plural/select
 * parameters such as "{count, plural, ...}" are captured, which the previous
 * simple-brace regex missed. Falls back to that regex for strings the ICU
 * parser rejects. Order is preserved (Set iteration is insertion order) rather
 * than sorted, so the generated `// params:` annotations stay stable.
 */
function extractParameters(str: string): string[] {
  const params = new Set<string>();

  try {
    extractParamsFromAST(parseICU(str), params);
  } catch {
    const regex = /\{(\w+)\}/g;
    let match;
    while ((match = regex.exec(str)) !== null) {
      params.add(match[1]);
    }
  }

  return [...params];
}

/**
 * Walks an ICU AST collecting parameter names, recursing into plural/select
 * option branches. Type 1 is a simple argument ({name}); type 6 is a
 * plural/select node ({count, plural, ...}); both carry the parameter name in
 * `value`. Mirrors extractParamsFromAST in validateTranslations.ts.
 */
function extractParamsFromAST(elements: ReturnType<typeof parseICU>, params: Set<string>): void {
  for (const element of elements) {
    const node = element as unknown as Record<string, unknown>;

    if ('type' in node && (node.type === 1 || node.type === 6) && typeof node.value === 'string') {
      params.add(node.value);
    }

    if ('options' in node && typeof node.options === 'object' && node.options !== null) {
      for (const option of Object.values(node.options as Record<string, unknown>)) {
        if (option && typeof option === 'object' && 'value' in option) {
          const optionValue = (option as Record<string, unknown>).value;
          if (Array.isArray(optionValue)) {
            extractParamsFromAST(optionValue as ReturnType<typeof parseICU>, params);
          }
        }
      }
    }
  }
}

/**
 * Generates parameter type definitions for translations with parameters
 */
function generateParamTypes(obj: TranslationObject, prefix = ''): string[] {
  const paramTypes: string[] = [];

  for (const [key, value] of Object.entries(obj)) {
    const fullKey = prefix ? `${prefix}.${key}` : key;

    if (isNestedObject(value)) {
      paramTypes.push(...generateParamTypes(value, fullKey));
    } else if (typeof value === 'string') {
      const params = extractParameters(value);
      if (params.length > 0) {
        const paramType = params.map(p => `${p}: string | number`).join('; ');
        paramTypes.push(`  '${fullKey}': { ${paramType} };`);
      }
    }
  }

  return paramTypes;
}

/**
 * Builds the contents of types.generated.ts from the English translation file:
 * - Union type of all available translation keys
 * - Parameter types for translations that require interpolation
 * - Type-safe translation function signature
 *
 * The output is formatted with the project Prettier config so it matches the
 * committed file byte-for-byte (the in-sync check relies on this). The key and
 * parameter counts are returned alongside it for the regeneration log.
 */
async function buildTypes(): Promise<GeneratedTypes> {
  const parsed: unknown = JSON.parse(readFileSync(MESSAGES_PATH, 'utf-8'));
  if (!isNestedObject(parsed)) {
    throw new Error(`${MESSAGES_PATH} did not parse to a JSON object`);
  }
  const enMessages = parsed;

  const translationKeys = generateTypeFromObject(enMessages);
  const paramTypes = generateParamTypes(enMessages);

  const tsContent = `/**
 * Auto-generated TypeScript types for i18n translation keys
 * Generated from: static/messages/en.json
 *
 * DO NOT EDIT THIS FILE MANUALLY
 * Run 'npm run generate:i18n-types' to regenerate
 */

/**
 * All available translation keys
 */
export type TranslationKey =
${translationKeys};

/**
 * Parameter types for translations that require parameters
 */
export type TranslationParams = {
${paramTypes.join('\n')}
};

/**
 * Helper type to get parameters for a specific translation key
 */
export type GetParams<K extends TranslationKey> = K extends keyof TranslationParams
  ? TranslationParams[K]
  : never;

/**
 * Type-safe translation function signature
 */
export interface TranslateFunction {
  <K extends TranslationKey>(
    key: K,
    ...args: GetParams<K> extends never ? [] : [GetParams<K>]
  ): string;
}
`;

  const prettierConfig = await prettier.resolveConfig(OUTPUT_PATH);
  const content = await prettier.format(tsContent, { ...prettierConfig, parser: 'typescript' });

  return {
    content,
    keyCount: translationKeys.split('\n').filter(Boolean).length,
    paramCount: paramTypes.length,
  };
}

/**
 * Regenerates types.generated.ts from the English translation file.
 */
async function writeTypes(): Promise<void> {
  const { content, keyCount, paramCount } = await buildTypes();
  writeFileSync(OUTPUT_PATH, content, 'utf-8');

  console.log(`✅ Generated TypeScript types at: ${OUTPUT_PATH}`);
  console.log(`📊 Total translation keys: ${keyCount}`);
  console.log(`📊 Keys with parameters: ${paramCount}`);
}

/**
 * Verifies types.generated.ts is in sync with en.json without writing it.
 * Exits with code 1 on drift so it can gate CI and the pre-commit hook,
 * matching the syncTranslations.ts --check pattern.
 */
async function checkTypes(): Promise<void> {
  const { content: expected } = await buildTypes();

  let actual: string;
  try {
    actual = readFileSync(OUTPUT_PATH, 'utf-8');
  } catch {
    console.error(`❌ ${OUTPUT_PATH} not found. Run 'npm run generate:i18n-types'.`);
    process.exit(1);
  }

  if (normalizeEol(expected) !== normalizeEol(actual)) {
    console.error('❌ types.generated.ts is out of sync with en.json.');
    console.error("   Run 'npm run generate:i18n-types' and commit the result.");
    process.exit(1);
  }

  console.log('✅ types.generated.ts is in sync with en.json');
}

// Run only when invoked directly (not when imported, e.g. by tests).
// pathToFileURL handles Windows path/URL differences (backslashes, drive letters);
// the typeof guard avoids throwing when argv[1] is undefined (e.g. some embeds).
if (
  typeof process.argv[1] === 'string' &&
  import.meta.url === pathToFileURL(process.argv[1]).href
) {
  const isCheck = process.argv.includes('--check');
  (isCheck ? checkTypes() : writeTypes()).catch(error => {
    console.error('❌ Error generating i18n types:', error);
    process.exit(1);
  });
}

export { buildTypes, extractParameters, generateTypeFromObject, generateParamTypes };
