/**
 * Every table header cell declares which way it labels: a plain `<th>` carries
 * `scope`, so assistive technology reads it as a column or row header.
 */
import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { basename, dirname, join } from 'node:path';

const libDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'lib');

/** Loading placeholder: its cells are bars standing in for rows, not headers. */
const PLACEHOLDER_FILES = new Set(['SkeletonDailySummary.svelte']);
const HTML_COMMENT = /<!--[\s\S]*?-->/g;
/** An opening `<th` tag across lines; `=>` inside an attribute does not end the tag. */
const TH_TAG = /<th(?=[\s>])(?:=>|[^>])*>/g;

describe('Table header scope', () => {
  it('gives every th in a component a scope attribute', () => {
    const offenders = readdirSync(libDir, { recursive: true, encoding: 'utf8' })
      .filter(file => file.endsWith('.svelte'))
      .filter(file => !PLACEHOLDER_FILES.has(basename(file)))
      // eslint-disable-next-line security/detect-non-literal-fs-filename -- paths come from listing the component directory
      .map(file => ({ file, text: readFileSync(join(libDir, file), 'utf8') }))
      .flatMap(({ file, text }) =>
        [...text.replace(HTML_COMMENT, '').matchAll(TH_TAG)]
          .filter(match => !/\sscope=/.test(match[0]))
          .map(match => `${file}: ${match[0].replace(/\s+/g, ' ')}`)
      );
    expect(offenders).toEqual([]);
  });
});
