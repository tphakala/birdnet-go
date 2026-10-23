import { describe, it, expect } from 'vitest';
import {
  mergeSettingsPatch,
  mergeSettingsPatchKeepingEdits,
  rebaseSettingsKeepingEdits,
} from './settingsMerge';

describe('mergeSettingsPatch', () => {
  it('merges nested objects key by key and keeps omitted keys', () => {
    const target = {
      audio: { source: 'a', gain: 3, export: { enabled: true, type: 'wav' } },
      x: 1,
    };
    const result = mergeSettingsPatch(target, {
      audio: { source: 'b', export: { enabled: false } },
    });
    expect(result).toEqual({
      audio: { source: 'b', gain: 3, export: { enabled: false, type: 'wav' } },
      x: 1,
    });
  });

  it('replaces arrays instead of merging them', () => {
    const target = { streams: [{ name: 'a', url: 'u', enabled: true }], health: { x: 1 } };
    const result = mergeSettingsPatch(target, { streams: [{ name: 'b' }] });
    expect(result.streams).toEqual([{ name: 'b' }]);
    expect(result.health).toEqual({ x: 1 });
  });

  it('replaces scalars and keeps an explicit null', () => {
    const result = mergeSettingsPatch({ a: 1, b: { c: 2 }, d: 'x' }, { a: 2, b: null, d: 'y' });
    expect(result).toEqual({ a: 2, b: null, d: 'y' });
  });

  it('skips keys whose patch value is undefined', () => {
    const result = mergeSettingsPatch({ a: 1, b: 2 }, { a: undefined, b: 3 });
    expect(result).toEqual({ a: 1, b: 3 });
  });

  it('ignores __proto__, constructor and prototype keys', () => {
    const patch = JSON.parse(
      '{"__proto__":{"polluted":true},"constructor":{"x":1},"prototype":{"y":1},"ok":1}'
    );
    const result = mergeSettingsPatch({}, patch);
    expect(result).toEqual({ ok: 1 });
    const probe: Record<string, unknown> = {};
    expect(probe.polluted).toBeUndefined();
    expect(Object.hasOwn(result, 'constructor')).toBe(false);
    expect(Object.hasOwn(result, 'prototype')).toBe(false);
  });

  it('does not mutate the target or the patch', () => {
    const target = { a: { b: 1 }, list: [1] };
    const patch = { a: { c: 2 }, list: [2, 3] };
    const targetCopy = JSON.parse(JSON.stringify(target));
    const patchCopy = JSON.parse(JSON.stringify(patch));
    const result = mergeSettingsPatch(target, patch);
    expect(target).toEqual(targetCopy);
    expect(patch).toEqual(patchCopy);
    expect(result.list).not.toBe(patch.list);
    expect(result.a).not.toBe(target.a);
  });

  it('treats an undefined target as empty', () => {
    expect(mergeSettingsPatch(undefined, { a: { b: 1 } })).toEqual({ a: { b: 1 } });
  });
});

describe('mergeSettingsPatchKeepingEdits', () => {
  it('keeping edits: takes the patch value where form equals base', () => {
    const base = { threshold: 0.8, name: 'n' };
    const form = { threshold: 0.8, name: 'n' };
    expect(mergeSettingsPatchKeepingEdits(form, base, { threshold: 0.9 })).toEqual({
      threshold: 0.9,
      name: 'n',
    });
  });

  it('keeping edits: keeps a form value that differs from base', () => {
    const base = { threshold: 0.8 };
    const form = { threshold: 0.5 };
    expect(mergeSettingsPatchKeepingEdits(form, base, { threshold: 0.9 })).toEqual({
      threshold: 0.5,
    });
  });

  it('keeping edits: recurses into nested objects and replaces arrays only when unedited', () => {
    const base = { a: { x: 1, y: 2, list: [1] }, other: { list: [1] } };
    const form = { a: { x: 5, y: 2, list: [1] }, other: { list: [9] } };
    const result = mergeSettingsPatchKeepingEdits(form, base, {
      a: { x: 10, y: 20, list: [2] },
      other: { list: [3] },
    });
    expect(result).toEqual({ a: { x: 5, y: 20, list: [2] }, other: { list: [9] } });
  });

  it('keeping edits: does not mutate its inputs and skips unsafe keys', () => {
    const base = { a: 1 };
    const form = { a: 1 };
    const patch = JSON.parse('{"__proto__":{"p":1},"a":2}');
    const result = mergeSettingsPatchKeepingEdits(form, base, patch);
    expect(result).toEqual({ a: 2 });
    expect(form).toEqual({ a: 1 });
    expect(base).toEqual({ a: 1 });
  });
});

describe('rebaseSettingsKeepingEdits', () => {
  type Case = {
    name: string;
    form: Record<string, unknown>;
    base: Record<string, unknown>;
    fresh: Record<string, unknown>;
    expected: Record<string, unknown>;
  };

  it.each<Case>([
    {
      name: 'an unedited leaf takes the server value while an edited sibling keeps the form value',
      form: { a: { x: 1, y: 'edited' } },
      base: { a: { x: 1, y: 'orig' } },
      fresh: { a: { x: 2, y: 'server' } },
      expected: { a: { x: 2, y: 'edited' } },
    },
    {
      name: 'an unedited key the server removed is dropped and an edited one is kept',
      form: { a: { path: 'old', note: 'mine' } },
      base: { a: { path: 'old', note: 'orig' } },
      fresh: { a: {} },
      expected: { a: { note: 'mine' } },
    },
    {
      name: 'a key the user deleted stays deleted',
      form: { map: { keep: 1 } },
      base: { map: { keep: 1, gone: 2 } },
      fresh: { map: { keep: 1, gone: 2 } },
      expected: { map: { keep: 1 } },
    },
    {
      name: 'an edited array is kept whole and an unedited array takes the server value',
      form: { edited: [1, 2], same: ['a'] },
      base: { edited: [1], same: ['a'] },
      fresh: { edited: [9], same: ['a', 'b'] },
      expected: { edited: [1, 2], same: ['a', 'b'] },
    },
  ])('$name', ({ form, base, fresh, expected }) => {
    expect(rebaseSettingsKeepingEdits(form, base, fresh)).toEqual(expected);
  });

  it('follows the server key order for unedited subtrees', () => {
    const base = { a: { x: 1, y: 2 } };
    const fresh = { a: { z: 3, y: 2, x: 1 } };
    const result = rebaseSettingsKeepingEdits({ a: { x: 1, y: 2 } }, base, fresh);
    expect(JSON.stringify(result)).toBe(JSON.stringify(fresh));
  });

  it('converges across successive server changes', () => {
    const base0 = { a: { edit: 0, one: 0, two: 0, gone: 'x' }, list: [1] };
    const form0 = { a: { edit: 5, one: 0, two: 0, gone: 'x' }, list: [1] };
    const fresh1 = { a: { edit: 0, one: 1, two: 0, gone: 'x' }, list: [1] };
    const r1 = rebaseSettingsKeepingEdits(form0, base0, fresh1);
    const fresh2 = { a: { edit: 0, one: 1, two: 2 }, list: [1, 2] };
    const r2 = rebaseSettingsKeepingEdits(r1, fresh1, fresh2);
    expect(r2).toEqual({ a: { edit: 5, one: 1, two: 2 }, list: [1, 2] });
    expect(rebaseSettingsKeepingEdits(r2, fresh2, fresh2)).toEqual(r2);
  });

  it('does not mutate its inputs or copy unsafe keys', () => {
    const form = { a: { x: 1 } };
    const base = { a: { x: 1 } };
    const fresh = JSON.parse('{"a":{"x":2,"list":[1]},"__proto__":{"polluted":true}}');
    const snapshot = JSON.stringify([form, base, fresh]);
    const result = rebaseSettingsKeepingEdits(form, base, fresh);
    expect(JSON.stringify([form, base, fresh])).toBe(snapshot);
    expect(Object.hasOwn(result, '__proto__')).toBe(false);
    // Assigning __proto__ on a plain object swaps its prototype instead of adding a key.
    expect(Object.getPrototypeOf(result)).toBe(Object.prototype);
    expect((result as Record<string, unknown>).polluted).toBeUndefined();
    expect((result.a as { list: number[] }).list).not.toBe(fresh.a.list);
  });
});
