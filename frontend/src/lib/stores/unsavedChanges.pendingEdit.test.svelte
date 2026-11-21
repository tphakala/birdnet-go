<!-- Test host: a draft held in a TextInput, which commits it on change (blur) -->
<script lang="ts">
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import { useUnsavedChangesGuard } from '$lib/utils/useUnsavedChangesGuard.svelte';

  let saved = 'saved';
  let draft = $state('saved');

  useUnsavedChangesGuard(() => draft !== saved);
</script>

<TextInput id="draft-name" label="Name" value={draft} onchange={value => (draft = value)} />
