<!-- Test host: the header menu with the shortcut cheat sheet, which the root layout mounts beside it -->
<script lang="ts">
  import HeaderSettingsMenu from './HeaderSettingsMenu.svelte';
  import KeyboardShortcutsModal from '$lib/desktop/components/modals/KeyboardShortcutsModal.svelte';
</script>

<HeaderSettingsMenu />
<KeyboardShortcutsModal />
