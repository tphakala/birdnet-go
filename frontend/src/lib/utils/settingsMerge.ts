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
 */
import { isPlainObject } from './security';

type PlainRecord = Record<string, unknown>;

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
