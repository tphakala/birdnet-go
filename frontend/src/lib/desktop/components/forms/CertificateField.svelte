<!--
  CertificateField Component
  PEM content textarea with file browse button for certificate/key input.
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import { toastActions } from '$lib/stores/toast';
  import { readFileAsText } from '$lib/utils/fileHelpers';

  interface Props {
    id: string;
    label: string;
    value: string;
    placeholder?: string;
    acceptFiles?: string;
    required?: boolean;
    disabled?: boolean;
    onchange: (_value: string) => void;
  }

  let {
    id,
    label,
    value,
    placeholder = '',
    acceptFiles = '.pem,.crt,.cer',
    required = false,
    disabled = false,
    onchange,
  }: Props = $props();

  async function handleFileSelect(event: Event) {
    if (disabled) return;
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    try {
      const content = await readFileAsText(file);
      onchange(content);
    } catch (err) {
      toastActions.error(err instanceof Error ? err.message : t('components.tls.fileReadError'));
    } finally {
      // Reset input so re-selecting the same file triggers the change event
      input.value = '';
    }
  }
</script>

<div>
  <label for={id} class="text-xs font-medium text-[var(--color-base-content)]/60 mb-1 block">
    {label}{#if required}
      *{/if}
  </label>
  <div class="flex gap-2">
    <textarea
      {id}
      {value}
      class="flex-1 px-3 py-2 rounded-lg text-sm bg-[var(--color-base-200)] border border-[var(--color-base-300)] font-mono resize-y min-h-[80px]"
      {placeholder}
      {required}
      {disabled}
      oninput={e => onchange((e.target as HTMLTextAreaElement).value)}
    ></textarea>
    <label
      class="px-3 py-2 rounded-lg text-xs font-medium bg-[var(--color-base-200)] border border-[var(--color-base-300)] transition-all self-start"
      class:cursor-pointer={!disabled}
      class:cursor-not-allowed={disabled}
      class:opacity-50={disabled}
    >
      {t('components.tls.browseFile')}
      <input
        type="file"
        accept={acceptFiles}
        class="hidden"
        onchange={handleFileSelect}
        {disabled}
      />
    </label>
  </div>
</div>
