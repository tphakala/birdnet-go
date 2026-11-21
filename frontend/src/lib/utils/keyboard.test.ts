import { describe, it, expect, afterEach } from 'vitest';
import {
  KEYS,
  describeBinding,
  hasCommandModifier,
  isArrowKeyWidget,
  isImeComposition,
  isApplePlatform,
  isInteractiveTarget,
  isTextEntryTarget,
  matchesBinding,
  shortcutTitle,
} from './keyboard';
import { t } from '$lib/i18n';

function keyEvent(key: string, init: KeyboardEventInit = {}): KeyboardEvent {
  return new KeyboardEvent('keydown', { key, ...init });
}

describe('matchesBinding', () => {
  it('ignores a key event that has no key', () => {
    // Some autofill and input method events reach a keydown listener without a key value
    const withoutKey = { shiftKey: false };

    for (const binding of [{ key: 'c' }, { key: KEYS.arrowLeft }, { key: KEYS.comma }]) {
      expect(matchesBinding(withoutKey, binding)).toBe(false);
    }
  });

  it.each([
    { name: 'letter', key: 'c', init: {}, binding: { key: 'c' }, expected: true },
    { name: 'Caps Lock letter', key: 'C', init: {}, binding: { key: 'c' }, expected: true },
    {
      name: 'Shift+C against plain c',
      key: 'C',
      init: { shiftKey: true },
      binding: { key: 'c' },
      expected: false,
    },
    {
      name: 'Shift letter binding',
      key: 'C',
      init: { shiftKey: true },
      binding: { key: 'c', shift: true },
      expected: true,
    },
    {
      name: '? with Shift held',
      key: '?',
      init: { shiftKey: true },
      binding: { key: KEYS.questionMark },
      expected: true,
    },
    {
      name: 'ArrowLeft against Shift+ArrowLeft binding',
      key: 'ArrowLeft',
      init: {},
      binding: { key: KEYS.arrowLeft, shift: true },
      expected: false,
    },
    {
      name: 'Shift+ArrowLeft against plain ArrowLeft binding',
      key: 'ArrowLeft',
      init: { shiftKey: true },
      binding: { key: KEYS.arrowLeft },
      expected: false,
    },
    {
      name: 'Shift+ArrowLeft against its binding',
      key: 'ArrowLeft',
      init: { shiftKey: true },
      binding: { key: KEYS.arrowLeft, shift: true },
      expected: true,
    },
    {
      name: 'Shift+Space against Space',
      key: ' ',
      init: { shiftKey: true },
      binding: { key: KEYS.space },
      expected: false,
    },
    {
      name: 'Shift+comma (key <) against comma',
      key: '<',
      init: { shiftKey: true },
      binding: { key: KEYS.comma },
      expected: false,
    },
    {
      name: 'comma',
      key: ',',
      init: {},
      binding: { key: KEYS.comma },
      expected: true,
    },
  ])('$name gives $expected', ({ key, init, binding, expected }) => {
    expect(matchesBinding(keyEvent(key, init), binding)).toBe(expected);
  });
});

describe('modifier and IME guards', () => {
  it.each([
    { name: 'Ctrl', init: { ctrlKey: true }, expected: true },
    { name: 'Meta', init: { metaKey: true }, expected: true },
    { name: 'Alt', init: { altKey: true }, expected: true },
    { name: 'Shift only', init: { shiftKey: true }, expected: false },
    { name: 'nothing', init: {}, expected: false },
  ])('hasCommandModifier with $name gives $expected', ({ init, expected }) => {
    expect(hasCommandModifier(keyEvent('c', init))).toBe(expected);
  });

  it.each([
    { name: 'isComposing', event: keyEvent('c', { isComposing: true }), expected: true },
    { name: 'key Process', event: keyEvent('Process'), expected: true },
    { name: 'keyCode 229', event: keyEvent('c', { keyCode: 229 }), expected: true },
    { name: 'a plain key', event: keyEvent('c', { keyCode: 67 }), expected: false },
  ])('isImeComposition with $name gives $expected', ({ event, expected }) => {
    expect(isImeComposition(event)).toBe(expected);
  });
});

describe('target classification', () => {
  afterEach(() => {
    document.body.replaceChildren();
  });

  function mount(html: string): Element {
    document.body.innerHTML = html;
    const target = document.body.querySelector('#target');
    if (!target) throw new Error('fixture has no #target');
    return target;
  }

  it.each([
    { name: 'text input', html: '<input id="target" type="text" />' },
    { name: 'input without a type', html: '<input id="target" />' },
    { name: 'search input', html: '<input id="target" type="search" />' },
    { name: 'textarea', html: '<textarea id="target"></textarea>' },
    { name: 'select', html: '<select id="target"><option>a</option></select>' },
    {
      name: 'child of a contenteditable element',
      html: '<div contenteditable="true"><span id="target">x</span></div>',
    },
    { name: 'empty contenteditable attribute', html: '<div id="target" contenteditable=""></div>' },
    { name: 'role=combobox', html: '<div id="target" role="combobox"></div>' },
  ])('$name is text entry', ({ html }) => {
    expect(isTextEntryTarget(mount(html))).toBe(true);
  });

  it.each([
    { name: 'switch checkbox', html: '<input id="target" type="checkbox" role="switch" />' },
    { name: 'range input', html: '<input id="target" type="range" />' },
    { name: 'radio input', html: '<input id="target" type="radio" />' },
    { name: 'button', html: '<button id="target">x</button>' },
    { name: 'plain div', html: '<div id="target"></div>' },
    {
      name: 'contenteditable=false',
      html: '<div contenteditable="false"><span id="target">x</span></div>',
    },
  ])('$name is not text entry', ({ html }) => {
    expect(isTextEntryTarget(mount(html))).toBe(false);
  });

  it.each([
    { name: 'range input', html: '<input id="target" type="range" />' },
    { name: 'radio input', html: '<input id="target" type="radio" />' },
    { name: 'icon inside role=slider', html: '<div role="slider"><svg id="target"></svg></div>' },
    {
      name: 'item inside a menu',
      html: '<div role="menu"><div id="target" role="menuitem"></div></div>',
    },
  ])('$name is an arrow-key widget', ({ html }) => {
    expect(isArrowKeyWidget(mount(html))).toBe(true);
  });

  it.each([
    { name: 'plain div', html: '<div id="target"></div>' },
    { name: 'button', html: '<button id="target">x</button>' },
  ])('$name is not an arrow-key widget', ({ html }) => {
    expect(isArrowKeyWidget(mount(html))).toBe(false);
  });

  it.each([
    { name: 'icon inside a button', html: '<button><svg id="target"></svg></button>' },
    { name: 'role=switch', html: '<div id="target" role="switch"></div>' },
    { name: 'link with href', html: '<a id="target" href="/x">x</a>' },
    { name: 'checkbox input', html: '<input id="target" type="checkbox" />' },
  ])('$name is interactive for Space', ({ html }) => {
    expect(isInteractiveTarget(mount(html))).toBe(true);
  });

  it.each([
    { name: 'plain div', html: '<div id="target"></div>' },
    { name: 'link without href', html: '<a id="target">x</a>' },
  ])('$name is not interactive for Space', ({ html }) => {
    expect(isInteractiveTarget(mount(html))).toBe(false);
  });

  it('treats window, document and a missing target as none of these', () => {
    for (const target of [window, document, null]) {
      expect(isTextEntryTarget(target)).toBe(false);
      expect(isArrowKeyWidget(target)).toBe(false);
      expect(isInteractiveTarget(target)).toBe(false);
    }
  });
});

describe('isApplePlatform', () => {
  const nav = (platform: string, userAgent = ''): Navigator => {
    // Only the two fields isApplePlatform reads are faked, so the object is asserted to Navigator
    const fake = { platform, userAgent };
    return fake as unknown as Navigator;
  };

  it.each([
    { name: 'a Mac', navigator: nav('MacIntel'), expected: true },
    { name: 'an iPhone', navigator: nav('iPhone'), expected: true },
    {
      name: 'an iPad reporting only its user agent',
      navigator: nav('', 'Mozilla iPad'),
      expected: true,
    },
    { name: 'Linux', navigator: nav('Linux x86_64', 'Mozilla X11'), expected: false },
    { name: 'Windows', navigator: nav('Win32', 'Mozilla Windows'), expected: false },
  ])('is $expected for $name', ({ navigator, expected }) => {
    expect(isApplePlatform(navigator)).toBe(expected);
  });
});

describe('describeBinding', () => {
  it('speaks a Shift combination, an arrow, a comma and a period as words', () => {
    expect(describeBinding({ key: KEYS.arrowLeft, shift: true }, false).label).toBe(
      'Shift plus Left Arrow'
    );
    expect(describeBinding({ key: KEYS.arrowRight }, false).label).toBe('Right Arrow');
    expect(describeBinding({ key: KEYS.comma }, false).label).toBe('Comma');
    expect(describeBinding({ key: KEYS.period }, false).label).toBe('Period');
  });

  const shift = t('shortcuts.keys.shift');
  const space = t('shortcuts.keys.space');

  it('describes Shift+ArrowLeft with the Shift word off Apple platforms', () => {
    const hint = describeBinding({ key: KEYS.arrowLeft, shift: true }, false);
    expect(hint.aria).toBe('Shift+ArrowLeft');
    expect(hint.keys).toEqual([shift, '←']);
    expect(hint.display).toBe(`${shift}+←`);
  });

  it('describes Shift+ArrowLeft with the Shift glyph on Apple platforms', () => {
    const hint = describeBinding({ key: KEYS.arrowLeft, shift: true }, true);
    expect(hint.aria).toBe('Shift+ArrowLeft');
    expect(hint.keys).toEqual(['⇧', '←']);
    // Apple glyphs sit together, as in the platform's own menus
    expect(hint.display).toBe('⇧←');
  });

  it('describes a letter with an upper-case cap and aria value', () => {
    const hint = describeBinding({ key: 'c' }, false);
    expect(hint.aria).toBe('C');
    expect(hint.keys).toEqual(['C']);
    expect(hint.label).toBe('C');
  });

  it('adds the secondary binding to aria only', () => {
    const hint = describeBinding({ key: 'p' }, false, { key: KEYS.space });
    expect(hint.aria).toBe('P Space');
    expect(hint.keys).toEqual(['P']);
    expect(hint.display).toBe('P');
    expect(describeBinding({ key: KEYS.space }, false).keys).toEqual([space]);
  });

  it('shows punctuation as its glyph and speaks it as a word', () => {
    const hint = describeBinding({ key: KEYS.questionMark }, false);
    expect(hint.aria).toBe('?');
    expect(hint.keys).toEqual(['?']);
    expect(hint.label).toBe(t('shortcuts.keys.questionMark'));
  });
});

describe('shortcutTitle', () => {
  it('names the key after the label, and gives no title without a hint', () => {
    const hint = describeBinding({ key: 'c' }, false);
    expect(shortcutTitle('Mark as correct', hint)).toBe('Mark as correct (C)');
    expect(shortcutTitle('Mark as correct', null)).toBeUndefined();
    expect(shortcutTitle('Mark as correct', undefined)).toBeUndefined();
  });
});
