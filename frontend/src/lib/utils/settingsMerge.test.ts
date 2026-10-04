import { describe, it, expect } from 'vitest';
import { mergeSettingsPatch, mergeSettingsPatchKeepingEdits } from './settingsMerge';

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
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
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
