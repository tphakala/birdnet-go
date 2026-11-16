/**
 * Merge helpers for applying a partial settings payload to the settings store.
 *
 * The semantics mirror the backend's section PATCH merge (deepMergeMaps in
 * internal/api/v2/settings.go): plain objects merge key by key, so keys the
 * patch omits are preserved; arrays and scalars replace the stored value; an
 * explicit null replaces it too. Keys whose value is undefined are skipped,
 * because JSON.stringify drops them and the server never sees them. Keeping the
 * store merge identical to the server merge is what keeps the store and
 * config.yaml in agreement after a section save.
 *
 * rebaseSettingsKeepingEdits is different: it rebases the working copy on a
 * full server snapshot, so it also drops keys the server no longer has.
 */
import { isPlainObject } from './security';

type PlainRecord = Record<string, unknown>;

/** A key path into a nested settings object. */
export type SettingsPath = readonly string[];

/** Keys that must never be copied, to rule out prototype pollution. */
const UNSAFE_KEYS = new Set(['__proto__', 'constructor', 'prototype']);

/** Deep-clones a JSON-like value (plain objects, arrays and primitives). */
function cloneValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(cloneValue);
  if (isPlainObject(value)) return cloneRecord(value);
  return value;
}

/** Deep-clones a plain object, dropping unsafe keys. */
function cloneRecord(value: PlainRecord): PlainRecord {
  const out: PlainRecord = {};
  for (const key of Object.keys(value)) {
    if (UNSAFE_KEYS.has(key)) continue;
    // eslint-disable-next-line security/detect-object-injection -- key comes from Object.keys of the value
    out[key] = cloneValue(value[key]);
  }
  return out;
}

/** Returns the own property value, or undefined when the key is not an own property. */
function ownValue(obj: PlainRecord, key: string): unknown {
  // eslint-disable-next-line security/detect-object-injection -- guarded by Object.hasOwn
  return Object.hasOwn(obj, key) ? obj[key] : undefined;
}

/**
 * Deep-merges patch into target and returns the result. Neither input is
 * mutated. An undefined target is treated as an empty object. This is the
 * three-way merge below with no pending edits (form equals base).
 */
export function mergeSettingsPatch(
  target: PlainRecord | undefined,
  patch: PlainRecord
): PlainRecord {
  return mergeSettingsPatchKeepingEdits(target, target, patch);
}

/**
 * Three-way merge for the working copy of the settings.
 * A leaf takes the patch value only when form still equals base there (no
 * pending edit); otherwise the form value is kept, so a save that resolves late
 * never overwrites something the user typed in the meantime. Objects present in
 * the patch, form and base are merged recursively. Equality is the JSON
 * comparison the store's unsaved-changes check uses. Neither input is mutated.
 */
export function mergeSettingsPatchKeepingEdits(
  form: PlainRecord | undefined,
  base: PlainRecord | undefined,
  patch: PlainRecord
): PlainRecord {
  const formObj = form ?? {};
  const baseObj = base ?? {};
  const result = cloneRecord(formObj);
  for (const key of Object.keys(patch)) {
    if (UNSAFE_KEYS.has(key)) continue;
    // eslint-disable-next-line security/detect-object-injection -- key comes from Object.keys of the patch
    const value = patch[key];
    if (value === undefined) continue;
    const formValue = ownValue(formObj, key);
    const baseValue = ownValue(baseObj, key);
    if (isPlainObject(value) && isPlainObject(formValue) && isPlainObject(baseValue)) {
      // eslint-disable-next-line security/detect-object-injection -- key is not an unsafe key
      result[key] = mergeSettingsPatchKeepingEdits(formValue, baseValue, value);
    } else if (JSON.stringify(formValue) === JSON.stringify(baseValue)) {
      // eslint-disable-next-line security/detect-object-injection -- key is not an unsafe key
      result[key] = cloneValue(value);
    }
  }
  return result;
}

/**
 * Rebases the working copy of the settings on a fresh full server snapshot.
 *
 * A path is edited when the form value differs from base there (the JSON
 * comparison the store's unsaved-changes check uses). An unedited path takes the
 * server value, and is omitted when the server no longer has the key. An edited
 * path keeps the form value, so a pending edit is never overwritten and a key the
 * user deleted stays deleted. Plain objects present on all three sides and edited
 * are merged recursively; arrays are whole values. Unedited subtrees are cloned
 * from the snapshot, so their key order follows the server. Keys are visited in
 * the snapshot's order, then keys only the form has. Neither input is mutated.
 *
 * A path that both the form and the server changed keeps the form value, so the
 * last save wins and the server change is overwritten without a warning. This is
 * deliberate. persistFormData, which saveSettings calls, rebases on a fresh GET
 * before every full-form send, so it holds for every such save and not only for
 * the resend after a 412; If-Match covers only the time between that GET and the
 * PUT. Reporting such overlaps (form differs
 * from base, fresh differs from base, and form differs from fresh) would need a
 * collector here and UI to show the list; neither exists.
 */
export function rebaseSettingsKeepingEdits(
  form: PlainRecord | undefined,
  base: PlainRecord | undefined,
  fresh: PlainRecord
): PlainRecord {
  const formObj = form ?? {};
  const baseObj = base ?? {};
  const result: PlainRecord = {};
  const keys = new Set([...Object.keys(fresh), ...Object.keys(formObj)]);
  for (const key of keys) {
    if (UNSAFE_KEYS.has(key)) continue;
    const formValue = ownValue(formObj, key);
    const baseValue = ownValue(baseObj, key);
    const freshValue = ownValue(fresh, key);
    if (JSON.stringify(formValue) === JSON.stringify(baseValue)) {
      if (Object.hasOwn(fresh, key)) {
        // eslint-disable-next-line security/detect-object-injection -- key is not an unsafe key
        result[key] = cloneValue(freshValue);
      }
    } else if (isPlainObject(formValue) && isPlainObject(baseValue) && isPlainObject(freshValue)) {
      // eslint-disable-next-line security/detect-object-injection -- key is not an unsafe key
      result[key] = rebaseSettingsKeepingEdits(formValue, baseValue, freshValue);
    } else if (Object.hasOwn(formObj, key)) {
      // eslint-disable-next-line security/detect-object-injection -- key is not an unsafe key
      result[key] = cloneValue(formValue);
    }
  }
  return result;
}

/**
 * The key paths of a partial settings object. Plain objects recurse; arrays,
 * scalars, null and empty objects are leaves, matching how the server merge
 * replaces them. Unsafe keys and undefined values are skipped.
 */
export function leafPaths(patch: PlainRecord): string[][] {
  const out: string[][] = [];
  const walk = (node: PlainRecord, prefix: string[]): void => {
    for (const key of Object.keys(node)) {
      if (UNSAFE_KEYS.has(key)) continue;
      // eslint-disable-next-line security/detect-object-injection -- key comes from Object.keys of the node
      const value = node[key];
      if (value === undefined) continue;
      if (isPlainObject(value) && Object.keys(value).length > 0) {
        walk(value, [...prefix, key]);
      } else {
        out.push([...prefix, key]);
      }
    }
  };
  walk(patch, []);
  return out;
}

/** Reads the own value at path, or undefined when any segment is missing. */
export function valueAt(obj: unknown, path: SettingsPath): unknown {
  let node = obj;
  for (const key of path) {
    if (!isPlainObject(node) || UNSAFE_KEYS.has(key)) return undefined;
    node = ownValue(node, key);
  }
  return node;
}

/**
 * Returns a copy of obj with value written at path, creating missing objects on
 * the way. Writing undefined removes the key. A path with an unsafe segment
 * changes nothing. Neither input is mutated.
 */
export function withValueAt(obj: PlainRecord, path: SettingsPath, value: unknown): PlainRecord {
  if (path.length === 0 || path.some(key => UNSAFE_KEYS.has(key))) return cloneRecord(obj);
  const [head, ...rest] = path;
  const result = cloneRecord(obj);
  if (rest.length === 0) {
    if (value === undefined) {
      return Object.fromEntries(Object.entries(result).filter(([key]) => key !== head));
    }
    // eslint-disable-next-line security/detect-object-injection -- head is checked against UNSAFE_KEYS
    result[head] = cloneValue(value);
    return result;
  }
  const child = ownValue(obj, head);
  // eslint-disable-next-line security/detect-object-injection -- head is checked against UNSAFE_KEYS
  result[head] = withValueAt(isPlainObject(child) ? child : {}, rest, value);
  return result;
}
