<!--
  Test fixture: a functional stand-in for TextInput.svelte.

  The real TextInput carries tooltip/validation machinery irrelevant to most
  parent-flow tests. This mock renders a plain native <input> that supports the
  same `bind:value` contract (via $bindable) so a test can type into it with
  fireEvent.input and drive a parent's validation/enablement logic. The
  `data-testid` mirrors the passed `id` so the field can be addressed directly.
  All other props are accepted and ignored.

  @component
-->
<script lang="ts">
  let {
    value = $bindable(''),
    id,
  }: {
    value?: string;
    id?: string;
    [key: string]: unknown;
  } = $props();
</script>

<input data-testid={id} {id} bind:value />
