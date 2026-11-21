<!--
  KeyboardShortcutsModal - the keyboard shortcut cheat sheet.

  Mounted once in the root layout. It opens from the header settings menu and with the
  `?` key, which it registers as the one shortcut that works on every page. It lists the
  shortcuts the current page registered (its section first, grouped), then the ones for
  all pages, and starts with the switch that turns every single-key shortcut off.

  @component
-->
<script lang="ts">
  import { onDestroy } from 'svelte';
  import Modal, { openDialogCount } from '$lib/desktop/components/ui/Modal.svelte';
  import ToggleField from '$lib/desktop/components/forms/ToggleField.svelte';
  import Kbd from '$lib/desktop/components/ui/Kbd.svelte';
  import { t } from '$lib/i18n';
  import {
    GLOBAL_SHORTCUT_SCOPE,
    HELP_SHORTCUT_ID,
    shortcuts,
    type ShortcutDefinition,
    type ShortcutGroup,
    type ShortcutScope,
  } from '$lib/stores/shortcuts.svelte';
  import { useShortcuts } from '$lib/utils/useShortcuts.svelte';
  import { KEYS, describeBinding, isApplePlatform } from '$lib/utils/keyboard';

  /** Order of the groups inside a section. */
  const GROUP_ORDER: readonly ShortcutGroup[] = ['navigation', 'review', 'audio', 'general'];

  interface SheetGroup {
    group: ShortcutGroup;
    definitions: ShortcutDefinition[];
  }

  interface SheetSection {
    scope: ShortcutScope;
    groups: SheetGroup[];
  }

  const apple = isApplePlatform();

  useShortcuts({
    id: GLOBAL_SHORTCUT_SCOPE,
    label: () => t('shortcuts.scopes.global'),
    definitions: [
      {
        id: HELP_SHORTCUT_ID,
        group: 'general',
        binding: { key: KEYS.questionMark },
        description: () => t('shortcuts.global.help'),
        // Inside a dialog only when the sheet is the one open: `?` must not close it
        // under a dialog that opened on top of it
        allowInDialog: () => shortcuts.helpOpen && openDialogCount() === 1,
        run: () => shortcuts.toggleHelp(),
      },
    ],
  });

  // The open state lives in the store; it must not outlive the sheet (the layout unmounts
  // on a switch to the login or error screen) or the sheet reopens on the next mount
  onDestroy(() => shortcuts.closeHelp());

  function groupLabel(group: ShortcutGroup): string {
    switch (group) {
      case 'navigation':
        return t('shortcuts.groups.navigation');
      case 'review':
        return t('shortcuts.groups.review');
      case 'audio':
        return t('shortcuts.groups.audio');
      case 'general':
        return t('shortcuts.groups.general');
    }
  }

  // Built only while open: visible() reads page state the closed sheet has no use for
  let sections = $derived<SheetSection[]>(
    shortcuts.helpOpen
      ? shortcuts.scopes
          .map(scope => {
            const listed = scope.definitions.filter(definition => definition.visible?.() ?? true);
            return {
              scope,
              groups: GROUP_ORDER.map(group => ({
                group,
                definitions: listed.filter(definition => definition.group === group),
              })).filter(entry => entry.definitions.length > 0),
            };
          })
          .filter(section => section.groups.length > 0)
      : []
  );
</script>

<Modal
  isOpen={shortcuts.helpOpen}
  title={t('shortcuts.modal.title')}
  size="md"
  scrollBody
  onClose={() => shortcuts.closeHelp()}
>
  <ToggleField
    label={t('shortcuts.modal.enable')}
    description={t('shortcuts.modal.enableHelp')}
    value={shortcuts.enabled}
    onUpdate={value => shortcuts.setEnabled(value)}
  />
  {#if !shortcuts.enabled}
    <p class="ks-off-note">{t('shortcuts.modal.offNote')}</p>
  {/if}

  {#each sections as section (section.scope)}
    <div class="ks-section">
      <h4 class="ks-section-title">{section.scope.label()}</h4>
      {#each section.groups as entry (entry.group)}
        <h5 class="ks-group-title">{groupLabel(entry.group)}</h5>
        <dl class="ks-list">
          {#each entry.definitions as definition (definition.id)}
            {@const primary = describeBinding(definition.binding, apple)}
            {@const secondary = definition.secondaryBinding
              ? describeBinding(definition.secondaryBinding, apple)
              : null}
            <div class="ks-row">
              <dt class="ks-description">{definition.description()}</dt>
              <dd class="ks-keys">
                <Kbd keys={primary.keys} label={primary.label} />
                {#if secondary}
                  <span class="ks-or">{t('shortcuts.modal.or')}</span>
                  <Kbd keys={secondary.keys} label={secondary.label} />
                {/if}
              </dd>
            </div>
          {/each}
        </dl>
      {/each}
    </div>
  {/each}
</Modal>

<style>
  .ks-off-note {
    margin-top: 0.5rem;
    font-size: 0.875rem;
    color: var(--text-muted);
  }

  .ks-section {
    margin-top: 1.25rem;
  }

  .ks-section-title {
    font-size: 0.9375rem;
    font-weight: 600;
  }

  /* Same look as the group headings in the species dialog */
  .ks-group-title {
    margin: 0.75rem 0 0.25rem;
    font-size: 0.75rem;
    font-weight: 600;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    color: var(--text-subtle);
  }

  .ks-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.375rem 0;
    border-top: 1px solid var(--border-100);
  }

  .ks-row:first-child {
    border-top: 0;
  }

  .ks-description {
    min-width: 0;
    font-size: 0.875rem;
  }

  .ks-keys {
    display: flex;
    flex-shrink: 0;
    align-items: center;
    gap: 0.375rem;
  }

  .ks-or {
    font-size: 0.75rem;
    color: var(--text-muted);
  }
</style>
