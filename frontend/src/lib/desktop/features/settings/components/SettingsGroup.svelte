<!--
  SettingsGroup

  Purpose: One group of settings on an immediately saved tab: a title row with
  the group's AutoSaveStatus on the right, then the controls.

  Props:
  - title: Group title
  - field: Auto-save field key of the group; its status shows beside the title
  - statusId: Id of the status message; while the group's field shows an error the
    group points aria-describedby at it
  - headingId: Id of the title heading; the group is labelled by it
  - labelFor: Renders the title as the label of this control id instead of a heading
  - required: Marks the labelled control as required
  - heading: Replaces the title text (for example a disclosure button)
  - class: Additional CSS classes (default spacing: space-y-3)
  - titleVariant: 'group' shows a heading title as a group label (default: 'field').
    A labelFor title and a heading snippet are not affected; style snippet content
    with SETTINGS_GROUP_LABEL_CLASSES
  - children: The controls

  @component
-->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import { autoSaveStatus } from '$lib/stores/settingsAutoSave.svelte';
  import AutoSaveStatus from './AutoSaveStatus.svelte';
  import { SETTINGS_GROUP_LABEL_CLASSES } from './SettingsGroupLabel.svelte';

  interface Props {
    title?: string;
    field?: string;
    statusId?: string;
    headingId?: string;
    labelFor?: string;
    required?: boolean;
    heading?: Snippet;
    class?: string;
    titleVariant?: 'field' | 'group';
    children?: Snippet;
  }

  let {
    title = '',
    field,
    statusId,
    headingId,
    labelFor,
    required = false,
    heading,
    class: className,
    titleVariant = 'field',
    children,
  }: Props = $props();

  const TITLE_CLASS = 'text-sm font-semibold text-[var(--color-base-content)]';

  let failed = $derived(field !== undefined && autoSaveStatus(field).state === 'error');
</script>

<section
  class={cn('min-w-0 space-y-3', className)}
  aria-labelledby={labelFor ? undefined : headingId}
  aria-describedby={failed ? statusId : undefined}
>
  <div class="flex min-h-6 items-center justify-between gap-3">
    {#if heading}
      {@render heading()}
    {:else if labelFor}
      <label for={labelFor} class={TITLE_CLASS}>
        {title}
        {#if required}
          <span class="ml-0.5 text-[var(--text-error)]" aria-hidden="true">*</span>
        {/if}
      </label>
    {:else}
      <h4
        id={headingId}
        class={titleVariant === 'group' ? SETTINGS_GROUP_LABEL_CLASSES : TITLE_CLASS}
      >
        {title}
      </h4>
    {/if}
    {#if field}
      <AutoSaveStatus {field} id={statusId} />
    {/if}
  </div>
  {@render children?.()}
</section>
