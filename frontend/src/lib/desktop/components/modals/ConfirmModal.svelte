<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import { t } from '$lib/i18n/index.js';

  interface Props {
    isOpen: boolean;
    title?: string;
    message?: string;
    confirmLabel?: string;
    cancelLabel?: string;
    confirmVariant?: 'primary' | 'secondary' | 'accent' | 'info' | 'success' | 'warning' | 'error';
    onClose: () => void;
    onConfirm: () => void | Promise<void>;
  }

  let {
    isOpen = false,
    title = t('common.modal.confirmAction'),
    message = t('common.modal.confirmMessage'),
    confirmLabel = t('common.buttons.confirm'),
    cancelLabel = t('common.buttons.cancel'),
    confirmVariant = 'error',
    onClose,
    onConfirm,
  }: Props = $props();
</script>

<Modal
  {isOpen}
  {title}
  type="confirm"
  size="sm"
  {confirmLabel}
  {cancelLabel}
  {confirmVariant}
  {onClose}
  {onConfirm}
  className="modal-bottom sm:modal-middle"
>
  {#snippet children()}
    <p>{message}</p>
  {/snippet}
</Modal>
