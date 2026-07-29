<script lang="ts">
  import { Pencil, Trash2, Save, X } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { api, ApiError } from '$lib/utils/api';
  import { isAuthenticated } from '$lib/utils/auth';
  import { toastActions } from '$lib/stores/toast';
  import { loggers } from '$lib/utils/logger';
  import { formatDate } from '$lib/utils/formatters';
  import type { SpeciesNoteData } from '$lib/types/species';

  const logger = loggers.ui;

  // Mirrors datastore.SpeciesNoteMaxLength, which is a BYTE limit. The textarea
  // maxlength is only a coarse character-count guard; the authoritative client
  // gate below measures UTF-8 byte length so multi-byte notes (e.g. CJK, emoji)
  // can't pass client validation only to be rejected by the server.
  const NOTE_MAX_BYTES = 10_000;
  // Mirrors datastore.SpeciesNotesMaxResults: the read endpoint returns at most
  // this many notes (newest first) with no pagination. When the response hits
  // the cap, older notes exist but are hidden — surface that instead of
  // presenting the capped list as complete.
  const NOTES_MAX_RESULTS = 500;
  const HTTP_BAD_REQUEST = 400;

  const utf8Encoder = new TextEncoder();
  function utf8ByteLength(value: string): number {
    return utf8Encoder.encode(value).length;
  }

  interface Props {
    scientificName: string;
    className?: string;
    [key: string]: unknown;
  }

  let { scientificName, className = '' }: Props = $props();

  const uid = $props.id();

  let notes = $state<SpeciesNoteData[]>([]);
  let loading = $state(true);
  // Boolean, not the error text: the raw message is server/network prose in
  // whatever language the backend or the browser produced it, and this component
  // translates everything else. The detail goes to the logger instead.
  let loadFailed = $state(false);

  let draft = $state('');
  let saving = $state(false);

  let editingId = $state<number | null>(null);
  let editDraft = $state('');
  let confirmingDeleteId = $state<number | null>(null);

  // The add form and the per-note edit form apply the same two gates to different
  // state, so the rules live here once rather than as two parallel $derived pairs
  // that can drift.
  function isTooLong(value: string): boolean {
    return utf8ByteLength(value.trim()) > NOTE_MAX_BYTES;
  }
  function canSubmit(value: string, tooLong: boolean): boolean {
    return value.trim().length > 0 && !tooLong && !saving;
  }
  // The reason a Save button is disabled, as required by the project's
  // no-ambiguous-disabled-states rule. Shared by both forms' title attribute and
  // the saveHelp snippet so the tooltip and the visible text cannot disagree.
  function saveBlockedReason(tooLong: boolean): string {
    return tooLong
      ? t('analytics.species.notes.tooLong', { max: NOTE_MAX_BYTES })
      : t('analytics.species.notes.saveDisabledReason');
  }

  let draftTooLong = $derived(isTooLong(draft));
  let canSave = $derived(canSubmit(draft, draftTooLong));

  let editTooLong = $derived(isTooLong(editDraft));
  let canSaveEdit = $derived(canSubmit(editDraft, editTooLong));

  async function load(name: string): Promise<void> {
    loading = true;
    loadFailed = false;
    try {
      const res =
        (await api.get<SpeciesNoteData[]>(`/api/v2/species/${encodeURIComponent(name)}/notes`)) ??
        [];
      // Guard against a stale request: the component instance is reused across
      // species, so if scientificName changed while this fetch was in flight, a
      // late resolution must not overwrite the current species' notes/state.
      if (name === scientificName.trim()) {
        notes = res;
      }
    } catch (e) {
      if (name === scientificName.trim()) {
        loadFailed = true;
      }
      logger.error('Failed to load species notes', e, { component: 'SpeciesNotes' });
    } finally {
      if (name === scientificName.trim()) {
        loading = false;
      }
    }
  }

  function handleWriteError(e: unknown, fallbackKey: string): void {
    // On a 400 the server reports the specific reason (too long, empty, invalid id)
    // via error_key, which api.ts surfaces as the already-localized ApiError
    // message. Show that rather than assuming every 400 is "too long"; other
    // failures use the action-specific fallback.
    if (e instanceof ApiError && e.status === HTTP_BAD_REQUEST) {
      toastActions.error(e.message);
    } else {
      toastActions.error(t(fallbackKey));
    }
    logger.error('Species note write failed', e, { component: 'SpeciesNotes' });
  }

  async function addNote(): Promise<void> {
    // Capture the species this write targets; the instance is reused across
    // species, so a switch mid-flight must not insert this note into another
    // species' list (load() drops stale reads; writes need the same guard).
    const currentName = scientificName.trim();
    const entry = draft.trim();
    if (entry.length === 0 || saving || utf8ByteLength(entry) > NOTE_MAX_BYTES) return;
    saving = true;
    try {
      const created = await api.post<SpeciesNoteData>(
        `/api/v2/species/${encodeURIComponent(currentName)}/notes`,
        { entry }
      );
      if (currentName !== scientificName.trim()) return;
      notes = [created, ...notes];
      draft = '';
    } catch (e) {
      handleWriteError(e, 'analytics.species.notes.saveFailed');
    } finally {
      saving = false;
    }
  }

  function startEdit(note: SpeciesNoteData): void {
    editingId = note.id;
    editDraft = note.entry;
  }

  function cancelEdit(): void {
    editingId = null;
    editDraft = '';
  }

  async function saveEdit(id: number): Promise<void> {
    const currentName = scientificName.trim();
    const entry = editDraft.trim();
    if (entry.length === 0 || saving || utf8ByteLength(entry) > NOTE_MAX_BYTES) return;
    saving = true;
    try {
      await api.put(`/api/v2/species/notes/${id}`, { entry });
      if (currentName !== scientificName.trim()) return;
      // Optimistically update the entry text only; the server-authoritative
      // updated_at refreshes on the next load (avoids a UTC toISOString date).
      notes = notes.map(n => (n.id === id ? { ...n, entry } : n));
      cancelEdit();
    } catch (e) {
      handleWriteError(e, 'analytics.species.notes.saveFailed');
    } finally {
      saving = false;
    }
  }

  async function deleteNote(id: number): Promise<void> {
    const currentName = scientificName.trim();
    confirmingDeleteId = null;
    // Guarded like addNote/saveEdit: without this a double-click issues two
    // DELETEs, and the second fails with a spurious "delete failed" toast.
    if (saving) return;
    saving = true;
    try {
      await api.delete(`/api/v2/species/notes/${id}`);
      if (currentName !== scientificName.trim()) return;
      notes = notes.filter(n => n.id !== id);
    } catch (e) {
      toastActions.error(t('analytics.species.notes.deleteFailed'));
      logger.error('Failed to delete species note', e, { component: 'SpeciesNotes' });
    } finally {
      saving = false;
    }
  }

  // Reload notes and reset all per-species edit state whenever the species
  // changes. The component instance is reused across species (e.g. when the
  // detail modal switches species), so loading only on mount would leave stale
  // notes and edit state pointing at the previous species.
  $effect(() => {
    const name = scientificName.trim();
    cancelEdit();
    confirmingDeleteId = null;
    draft = '';
    // Notes are auth-gated for reads too, so never fetch for an anonymous user
    // (the request would 401). Parents also gate the section on auth; this is a
    // standalone safeguard. Reactive on $isAuthenticated, so login refetches.
    if (!name || !$isAuthenticated) {
      notes = [];
      loading = false;
      loadFailed = false;
      return;
    }
    void load(name);
  });
</script>

<!--
  The disabled-Save explanation, rendered identically under the add form and under
  every open edit form (which previously carried byte-identical copies).

  Show the "too long" reason visibly — tooltips are invisible on touch devices —
  while the empty-draft reason stays screen-reader-only, since an empty textarea
  already makes it self-evident. `helpId` is the caller's instance-scoped id, which
  is what its button points `aria-describedby` at.
-->
{#snippet saveHelp(helpId: string, tooLong: boolean)}
  <p id={helpId} class={tooLong ? 'text-xs text-[var(--color-error)] mt-1' : 'sr-only'}>
    {saveBlockedReason(tooLong)}
  </p>
{/snippet}

<section class={`species-notes ${className}`} aria-label={t('analytics.species.notes.title')}>
  <h3 class="text-base font-semibold mb-2">{t('analytics.species.notes.title')}</h3>

  {#if $isAuthenticated}
    <div class="mb-3">
      <label for={`${uid}-draft`} class="sr-only">{t('analytics.species.notes.title')}</label>
      <textarea
        id={`${uid}-draft`}
        class="textarea textarea-bordered w-full text-sm"
        rows="2"
        maxlength={NOTE_MAX_BYTES}
        placeholder={t('analytics.species.notes.placeholder')}
        bind:value={draft}></textarea>
      <div class="mt-1 flex justify-end">
        <button
          type="button"
          class="btn btn-primary btn-sm"
          disabled={!canSave}
          title={!canSave ? saveBlockedReason(draftTooLong) : undefined}
          aria-describedby={!canSave ? `${uid}-save-help` : undefined}
          onclick={addNote}
        >
          <Save class="h-4 w-4" />
          {t('analytics.species.notes.save')}
        </button>
      </div>
      {#if !canSave}
        {@render saveHelp(`${uid}-save-help`, draftTooLong)}
      {/if}
    </div>
  {/if}

  {#if loading}
    <div role="status" aria-live="polite" class="text-sm text-[var(--color-base-content)]/70 py-2">
      {t('common.loading')}
    </div>
  {:else if loadFailed}
    <div
      role="alert"
      class="p-3 rounded-lg bg-[var(--color-error)]/10 text-[var(--color-error)] text-sm"
    >
      {t('analytics.species.notes.loadFailed')}
    </div>
  {:else if notes.length === 0}
    <p class="text-sm text-[var(--color-base-content)]/70">{t('analytics.species.notes.empty')}</p>
  {:else}
    {#if notes.length >= NOTES_MAX_RESULTS}
      <!-- A full page means the server cap was hit: older notes are not shown
           (an exactly-at-cap total shows this too — harmless). -->
      <p role="status" class="text-xs text-[var(--color-base-content)]/70 mb-2">
        {t('analytics.species.notes.truncated', { max: NOTES_MAX_RESULTS })}
      </p>
    {/if}
    <ul class="space-y-2">
      {#each notes as note (note.id)}
        <li class="rounded-md border border-[var(--color-base-300)] p-2">
          {#if editingId === note.id}
            <label for={`${uid}-edit-${note.id}`} class="sr-only">
              {t('analytics.species.notes.editLabel')}
            </label>
            <textarea
              id={`${uid}-edit-${note.id}`}
              class="textarea textarea-bordered w-full text-sm"
              rows="2"
              maxlength={NOTE_MAX_BYTES}
              bind:value={editDraft}></textarea>
            <div class="mt-1 flex justify-end gap-2">
              <button
                type="button"
                class="btn btn-ghost btn-sm"
                onclick={cancelEdit}
                aria-label={t('common.close')}
              >
                <X class="h-4 w-4" />
              </button>
              <button
                type="button"
                class="btn btn-primary btn-sm"
                disabled={!canSaveEdit}
                title={!canSaveEdit ? saveBlockedReason(editTooLong) : undefined}
                aria-describedby={!canSaveEdit ? `${uid}-edit-save-help` : undefined}
                onclick={() => saveEdit(note.id)}
              >
                <Save class="h-4 w-4" />
                {t('analytics.species.notes.save')}
              </button>
            </div>
            {#if !canSaveEdit}
              {@render saveHelp(`${uid}-edit-save-help`, editTooLong)}
            {/if}
          {:else}
            <p class="text-sm whitespace-pre-line">{note.entry}</p>
            <div class="mt-1 flex items-center justify-between gap-2">
              <span class="text-xs text-[var(--color-base-content)]/50"
                >{formatDate(note.updated_at)}</span
              >
              {#if $isAuthenticated}
                {#if confirmingDeleteId === note.id}
                  <div class="flex items-center gap-2">
                    <span class="text-xs text-[var(--color-base-content)]/70">
                      {t('analytics.species.notes.deleteConfirm')}
                    </span>
                    <button
                      type="button"
                      class="btn btn-error btn-xs"
                      onclick={() => deleteNote(note.id)}
                    >
                      {t('common.delete')}
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs"
                      onclick={() => (confirmingDeleteId = null)}
                    >
                      {t('common.cancel')}
                    </button>
                  </div>
                {:else}
                  <div class="flex gap-1">
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs"
                      aria-label={t('analytics.species.notes.editLabel')}
                      onclick={() => startEdit(note)}
                    >
                      <Pencil class="h-3.5 w-3.5" />
                    </button>
                    <button
                      type="button"
                      class="btn btn-ghost btn-xs text-[var(--color-error)]"
                      aria-label={t('analytics.species.notes.deleteConfirm')}
                      onclick={() => (confirmingDeleteId = note.id)}
                    >
                      <Trash2 class="h-3.5 w-3.5" />
                    </button>
                  </div>
                {/if}
              {/if}
            </div>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</section>
