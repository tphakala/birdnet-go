/**
 * Source guard: every SelectDropdown (and LanguageSelector, which wraps it) in the app must
 * have an accessible name that does not depend on the selected value. The trigger shows the
 * selected option as its text, and a `role="combobox"` trigger does not take its name from
 * that text, so an unnamed use ends up nameless (axe `button-name`).
 *
 * A use is named when its tag has one of:
 * - the `label` prop (visible label rendered by the component),
 * - the `aria-label` prop,
 * - an `id` that a `<label for>` (or a FormField `id`) in the same file points at.
 */
import { describe, it, expect } from 'vitest';

const sources = import.meta.glob<string>('/src/**/*.svelte', {
  query: '?raw',
  import: 'default',
  eager: true,
});

interface ParsedAttribute {
  name: string;
  /** Raw value: the text between the quotes or braces, or undefined for a bare attribute */
  value?: string;
}

interface ParsedTag {
  component: string;
  line: number;
  attributes: ParsedAttribute[];
}

/**
 * Index of the brace that closes the one opened at `start`, skipping nested braces. Throws when
 * there is none, so a malformed source fails the test instead of looping.
 */
function closingBrace(source: string, start: number): number {
  let depth = 0;
  for (let i = start; i < source.length; i++) {
    const char = source.charAt(i);
    if (char === '{') depth++;
    else if (char === '}' && --depth === 0) return i;
  }
  throw new Error(`unclosed brace at offset ${start}`);
}

/** Parses the attributes of a tag whose text starts right after the component name. */
function parseAttributes(source: string, from: number): { attributes: ParsedAttribute[] } {
  const attributes: ParsedAttribute[] = [];
  let i = from;
  while (i < source.length) {
    const char = source.charAt(i);
    if (/\s/.test(char)) {
      i++;
    } else if (char === '/' || char === '>') {
      break;
    } else if (char === '{') {
      // Shorthand attribute such as {id}, or a spread such as {...rest}
      const end = closingBrace(source, i);
      const inner = source.slice(i + 1, end).trim();
      if (!inner.startsWith('...')) attributes.push({ name: inner, value: `{${inner}}` });
      i = end + 1;
    } else {
      const nameMatch = /^[^\s=/>{]+/.exec(source.slice(i));
      if (!nameMatch) break;
      const name = nameMatch[0];
      i += name.length;
      if (source.charAt(i) !== '=') {
        attributes.push({ name });
        continue;
      }
      i++;
      const quote = source.charAt(i);
      if (quote === '{') {
        const end = closingBrace(source, i);
        attributes.push({ name, value: source.slice(i + 1, end).trim() });
        i = end + 1;
      } else if (quote === '"' || quote === "'") {
        const end = source.indexOf(quote, i + 1);
        if (end < 0) throw new Error(`unclosed quote at offset ${i}`);
        attributes.push({ name, value: source.slice(i + 1, end) });
        i = end + 1;
      } else {
        const bare = /^[^\s/>]+/.exec(source.slice(i));
        attributes.push({ name, value: bare?.[0] });
        i += bare?.[0].length ?? 0;
      }
    }
  }
  return { attributes };
}

/** Finds every wrapped-component tag in a Svelte source (HTML comments are ignored). */
function findTags(source: string): ParsedTag[] {
  const text = source.replace(/<!--[\s\S]*?-->/g, match => match.replace(/[^\n]/g, ' '));
  const tags: ParsedTag[] = [];
  const pattern = /<(SelectDropdown|LanguageSelector)(?=[\s/>])/g;
  for (const match of text.matchAll(pattern)) {
    const start = match.index + match[0].length;
    tags.push({
      component: match[1],
      line: text.slice(0, match.index).split('\n').length,
      attributes: parseAttributes(text, start).attributes,
    });
  }
  return tags;
}

/** Values of every `for=` attribute and every FormField `id` in a source, as raw text. */
function labelTargets(source: string): string[] {
  const text = source.replace(/<!--[\s\S]*?-->/g, '');
  const targets: string[] = [];
  const forPattern = /\sfor=("[^"]*"|'[^']*'|\{)/g;
  for (const match of text.matchAll(forPattern)) {
    if (match[1] === '{') {
      const open = match.index + match[0].length - 1;
      targets.push(text.slice(open + 1, closingBrace(text, open)));
    } else {
      targets.push(match[1].slice(1, -1));
    }
  }
  for (const match of text.matchAll(/<FormField\b/g)) {
    const { attributes } = parseAttributes(text, match.index + match[0].length);
    const id = attributes.find(attribute => attribute.name === 'id');
    // FormField renders its <label for> only when it has a label
    if (id?.value && attributes.some(attribute => attribute.name === 'label')) {
      targets.push(id.value);
    }
  }
  return targets;
}

/** True when `target` names `id` as a whole token, not as part of a longer id. */
function pointsAt(target: string, id: string): boolean {
  const isIdChar = (char: string) => /[\w-]/.test(char);
  for (let at = target.indexOf(id); at >= 0; at = target.indexOf(id, at + 1)) {
    const before = at === 0 ? '' : target.charAt(at - 1);
    const after = target.charAt(at + id.length);
    if (!isIdChar(before) && !isIdChar(after)) return true;
  }
  return false;
}

/** Why a tag has no value-independent accessible name, or null when it has one. */
function namingProblem(tag: ParsedTag, targets: string[]): string | null {
  const names = new Set(tag.attributes.map(attribute => attribute.name));
  if (names.has('label') || names.has('aria-label')) return null;

  const id = tag.attributes.find(attribute => attribute.name === 'id');
  if (!id) return 'has no label, aria-label or id';
  const idText = (id.value ?? '').replace(/^\{|\}$/g, '').replace(/^['"]|['"]$/g, '');
  if (idText && targets.some(target => pointsAt(target, idText))) return null;
  return `has id ${id.value} but no <label for> or FormField id points at it in this file`;
}

/** Runs a scan of one source and names the file when the scan throws on malformed markup. */
function inFile<T>(path: string, scan: () => T): T {
  try {
    return scan();
  } catch (error) {
    throw new Error(`${path}: ${error instanceof Error ? error.message : String(error)}`, {
      cause: error,
    });
  }
}

describe('SelectDropdown naming guard', () => {
  const productionFiles = Object.entries(sources).filter(
    ([path]) => !path.endsWith('.test.svelte') && !path.includes('/test/')
  );

  it('finds the dropdown uses it is meant to guard', () => {
    const count = productionFiles.reduce(
      (sum, [path, source]) => sum + inFile(path, () => findTags(source)).length,
      0
    );
    expect(count).toBeGreaterThan(50);
  });

  it('gives every SelectDropdown use a name that does not depend on the selected value', () => {
    const problems: string[] = [];
    for (const [path, source] of productionFiles) {
      const targets = inFile(path, () => labelTargets(source));
      for (const tag of inFile(path, () => findTags(source))) {
        const problem = namingProblem(tag, targets);
        if (problem) problems.push(`${path}:${tag.line} <${tag.component}> ${problem}`);
      }
    }
    expect(problems).toEqual([]);
  });

  describe('analyzer', () => {
    const problemsIn = (source: string) => {
      const targets = labelTargets(source);
      return findTags(source).map(tag => namingProblem(tag, targets));
    };

    it('flags an unnamed dropdown', () => {
      expect(problemsIn('<SelectDropdown options={opts} value={v} />')).toEqual([
        'has no label, aria-label or id',
      ]);
    });

    it('flags an id that no label points at', () => {
      const [problem] = problemsIn('<label for="other">A</label><SelectDropdown id="mine" />');
      expect(problem).toContain('has id mine but no <label for>');
    });

    it('accepts label, aria-label and id-linked labels', () => {
      expect(
        problemsIn(`
          <SelectDropdown label={t('a')} />
          <SelectDropdown aria-label={t('b')} />
          <label for="x">X</label><SelectDropdown id="x" />
          <label for={\`\${s}-month\`}>M</label><SelectDropdown id={\`\${s}-month\`} />
          <FormField label="F" id="period"><SelectDropdown id="period" /></FormField>
          <LanguageSelector id="lang" /><label for="lang">L</label>
        `)
      ).toEqual([null, null, null, null, null, null]);
    });

    it('flags an id that is only part of a label target', () => {
      const [shorter] = problemsIn('<label for="timePeriod">T</label><SelectDropdown id="time" />');
      expect(shorter).toContain('has id time but no <label for>');
      const [dashed] = problemsIn(
        '<label for="notification-type">T</label><SelectDropdown id="type" />'
      );
      expect(dashed).toContain('has id type but no <label for>');
    });

    it('does not count a FormField without a label as a label target', () => {
      const [problem] = problemsIn('<FormField id="p"><SelectDropdown id="p" /></FormField>');
      expect(problem).toContain('has id p but no <label for>');
    });

    it('accepts an id used inside a conditional for expression', () => {
      expect(
        problemsIn(`
          <label for={ready ? FIELD_ID : undefined}>F</label><SelectDropdown id={FIELD_ID} />
          <label for={loading ? undefined : 'wizard-field'}>W</label><SelectDropdown id="wizard-field" />
        `)
      ).toEqual([null, null]);
    });

    it('throws instead of looping on an unclosed brace or quote', () => {
      expect(() => findTags('<SelectDropdown onChange={f(')).toThrow('unclosed');
      expect(() => findTags('<SelectDropdown id="mine')).toThrow('unclosed');
      expect(() => labelTargets('<FormField label={t(')).toThrow('unclosed');
    });

    it('names the file when a scan throws', () => {
      expect(() =>
        inFile('src/Broken.svelte', () => findTags('<SelectDropdown onChange={f('))
      ).toThrow('src/Broken.svelte: unclosed brace');
    });

    it('ignores a dropdown mentioned in an HTML comment and attributes inside handlers', () => {
      expect(
        problemsIn(
          '<!-- <SelectDropdown /> --><SelectDropdown aria-label="n" onChange={label => f(label)} />'
        )
      ).toEqual([null]);
      expect(problemsIn('<SelectDropdown onChange={label => f(label)} />')).toEqual([
        'has no label, aria-label or id',
      ]);
    });
  });
});
