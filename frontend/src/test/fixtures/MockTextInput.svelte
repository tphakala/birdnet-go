<!--
  Test fixture: a functional stand-in for TextInput.svelte.

  The real TextInput carries tooltip/validation machinery irrelevant to most
  parent-flow tests. This mock renders a plain native <input> that supports the
  same `bind:value` contract (via $bindable) so a test can type into it with
  fireEvent.input and drive a parent's validation/enablement logic. The
  `data-testid` mirrors the passed `id` so the field can be addressed directly.
  The `maxlength` prop is forwarded to the input; all other props are accepted and
  ignored.

  @component
-->
<script lang="ts">
  interface MockTextInputProps {
    value?: string;
    id?: string;
    maxlength?: number;
    [key: string]: unknown;
  }

  let { value = $bindable(''), id, maxlength }: MockTextInputProps = $props();
</script>

<input data-testid={id} {id} {maxlength} bind:value />
