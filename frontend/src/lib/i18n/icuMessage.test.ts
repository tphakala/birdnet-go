import { describe, it, expect } from 'vitest';
import { extractICUParameters, findICUSyntaxError } from './icuMessage';
import { extractParameters } from './generateTypes';

describe('extractICUParameters', () => {
  it('extracts simple parameters in order of appearance', () => {
    expect(extractICUParameters('Hello {name}, you have {count} messages')).toEqual([
      'name',
      'count',
    ]);
  });

  it('extracts a parameter inside an HTML tag attribute', () => {
    expect(extractICUParameters('See <a href="{url}" class="link">the docs</a>')).toEqual(['url']);
  });

  it('extracts a parameter inside HTML tag content', () => {
    expect(extractICUParameters('<strong>{name}</strong> was detected')).toEqual(['name']);
  });

  it('extracts plural and select parameters together with tag parameters', () => {
    expect(
      extractICUParameters(
        '<a href="{url}">{count, plural, one {# item} other {# items}}</a> for {kind, select, bird {birds} other {others}}'
      )
    ).toEqual(['url', 'count', 'kind']);
  });

  it('extracts parameters nested inside plural branches', () => {
    expect(
      extractICUParameters('{count, plural, one {{name} saw # bird} other {{name} saw # birds}}')
    ).toEqual(['count', 'name']);
  });

  it('returns no parameters for plain HTML without placeholders', () => {
    expect(
      extractICUParameters('Visit <a href="https://example.com" target="_blank">Example</a>.')
    ).toEqual([]);
  });

  it('falls back to simple placeholders when the value is not valid ICU', () => {
    expect(extractICUParameters('Hi {{.CommonName}} and {name}')).toEqual(['name']);
  });
});

describe('findICUSyntaxError', () => {
  it('accepts HTML tags with attributes, which the runtime renders as literal HTML', () => {
    expect(
      findICUSyntaxError(
        'Sign up at <a href="https://example.com" class="link link-primary" target="_blank" rel="noopener noreferrer">Example</a>.'
      )
    ).toBeNull();
  });

  it('accepts a parameter inside a tag attribute', () => {
    expect(findICUSyntaxError('<a href="{url}">link</a>')).toBeNull();
  });

  it('accepts valid plural syntax', () => {
    expect(findICUSyntaxError('{count, plural, one {# item} other {# items}}')).toBeNull();
  });

  it('reports malformed ICU syntax', () => {
    expect(findICUSyntaxError('{count, plural, one {# item}')).not.toBeNull();
  });

  it('reports malformed ICU syntax inside HTML markup', () => {
    expect(findICUSyntaxError('<strong>{count, plural, one {# item}</strong>')).not.toBeNull();
  });

  it('accepts values containing Go template field references', () => {
    expect(
      findICUSyntaxError('e.g. First detection of {{.CommonName}} ({{.ScientificName}})')
    ).toBeNull();
  });

  it('still checks the ICU part of a value that also has Go template references', () => {
    expect(findICUSyntaxError('{{.CommonName}} {count, plural, one {# item}')).not.toBeNull();
  });

  it('does not mistake an ICU plural branch holding a parameter for a Go template', () => {
    expect(findICUSyntaxError('{count, plural, one {# item} other {{name}}}')).toBeNull();
  });

  it('reports arguments the runtime t() cannot render', () => {
    expect(findICUSyntaxError('{kind, select, bird {Bird} other {Other}}')).toMatch(/select/);
    expect(findICUSyntaxError('{n, selectordinal, one {#st} other {#th}}')).toMatch(
      /selectordinal/
    );
    expect(findICUSyntaxError('{n, plural, offset:1 one {# x} other {# y}}')).toMatch(/offset/);
    expect(findICUSyntaxError('{n, number}')).toMatch(/number/);
    expect(findICUSyntaxError('{d, date, short}')).toMatch(/date/);
    expect(findICUSyntaxError('{d, time, short}')).toMatch(/time/);
    expect(
      findICUSyntaxError('{count, plural, one {# {kind, select, a {A} other {B}}} other {#}}')
    ).toMatch(/select/);
  });

  it('treats an apostrophe as a literal, as the runtime does', () => {
    // ICU quoting would hide the unclosed brace after the apostrophe.
    expect(findICUSyntaxError("l'{name")).not.toBeNull();
  });
});

describe('extractICUParameters with runtime-literal characters', () => {
  it('finds a parameter after an apostrophe', () => {
    expect(extractICUParameters("l'{name}")).toEqual(['name']);
    expect(extractICUParameters("d'<strong>{name}</strong>")).toEqual(['name']);
  });

  it('finds parameters next to Go template field references', () => {
    expect(
      extractICUParameters('{{.CommonName}}: {count, plural, one {# bird} other {# birds}}')
    ).toEqual(['count']);
  });
});

describe('generateTypes extractParameters', () => {
  it('uses the shared extractor, so parameters inside tags get types', () => {
    expect(extractParameters('<a href="{url}">{label}</a>')).toEqual(['url', 'label']);
  });
});
