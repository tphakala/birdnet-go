/**
 * Local draft of a group of settings that is saved with a scoped Apply (device
 * and sample rate, a stream's connection), because a change restarts something.
 *
 * The draft follows the saved values until the user edits it. Apply commits it
 * through the caller's commit function under one auto-save field; a save hands
 * the draft back to the saved values, a failure keeps it so the user can apply
 * again or cancel. Construct it during component initialisation: it registers
 * two effects that live as long as the component.
 */
import { untrack } from 'svelte';
import { autoSaveStatus, type AutoSaveFieldStatus } from '$lib/stores/settingsAutoSave.svelte';

/** True when two drafts hold the same value at every key. */
export function sameDraft<T extends object>(a: T, b: T): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const key of keys) {
    if (Reflect.get(a, key) !== Reflect.get(b, key)) return false;
  }
  return true;
}

export class ApplyDraft<T extends object> {
  /** The values shown in the controls. */
  draft: T;
  /** Set by an edit; the draft then no longer follows the saved values. */
  edited = $state(false);
  #pending = $state(false);
  readonly #saved: () => T;
  readonly #field: string;

  /**
   * @param saved - Reads the saved values; read reactively
   * @param field - Auto-save field key the apply commits under
   */
  constructor(saved: () => T, field: string) {
    this.#saved = saved;
    this.#field = field;
    this.draft = $state(untrack(() => ({ ...saved() })));

    // Follows the saved values while the draft is not being edited.
    $effect(() => {
      const current = this.#saved();
      untrack(() => {
        if (!this.edited) this.draft = { ...current };
      });
    });

    // Ends an apply: a save hands the draft back to the saved values, a failure keeps it.
    $effect(() => {
      const state = this.status.state;
      if (!this.#pending || state === 'saving') return;
      untrack(() => {
        this.#pending = false;
        if (state === 'error') return;
        this.edited = false;
        this.draft = { ...this.#saved() };
      });
    });
  }

  /** The auto-save status of the field the apply commits under. */
  get status(): AutoSaveFieldStatus {
    return autoSaveStatus(this.#field);
  }

  /** The field key the apply commits under. */
  get field(): string {
    return this.#field;
  }

  /** True while the draft differs from the saved values. */
  get dirty(): boolean {
    return !sameDraft(this.draft, this.#saved());
  }

  /** True while an apply is being saved. */
  get applying(): boolean {
    return this.#pending && this.status.state === 'saving';
  }

  /** True when the last apply failed. */
  get failed(): boolean {
    return this.status.state === 'error';
  }

  /**
   * True while the Apply group shows: the draft differs, or an apply runs (staging
   * makes the saved values equal the draft until the result arrives).
   */
  get visible(): boolean {
    return this.dirty || this.applying;
  }

  /** Changes the draft and stops it following the saved values. */
  set(patch: Partial<T>): void {
    this.draft = { ...this.draft, ...patch };
    this.edited = true;
  }

  /** Drops the draft and returns to the saved values. */
  cancel(): void {
    if (this.applying) return;
    this.edited = false;
    this.draft = { ...this.#saved() };
  }

  /** Commits the draft; ignored while an apply runs. */
  apply(commit: (draft: T) => void): void {
    if (this.applying) return;
    this.#pending = true;
    commit({ ...this.draft });
  }
}
