<script lang="ts">
  import { cn } from '$lib/utils/cn.js';
  import type { HTMLAttributes } from 'svelte/elements';
  import { actionIcons, alertIconsSvg, navigationIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts
  import { validateCIDR, IndexMap, safeArrayAccess } from '$lib/utils/security';

  interface Props extends HTMLAttributes<HTMLDivElement> {
    label: string;
    subnets: string[];
    onUpdate: (_subnets: string[]) => void;
    helpText?: string;
    disabled?: boolean;
    error?: string;
    className?: string;
    placeholder?: string;
    maxItems?: number;
    emptyStateMessage?: string;
  }

  let {
    label,
    subnets,
    onUpdate,
    helpText = '',
    disabled = false,
    error,
    className = '',
    placeholder = '192.168.1.0/24',
    maxItems = 10,
    emptyStateMessage = 'Add subnet ranges to configure network access',
    ...rest
  }: Props = $props();

  let newSubnet = $state('');
  let fieldId = `subnet-${Math.random().toString(36).substring(2, 11)}`;
  let errors = $state(new IndexMap<string>());

  // Validation function for CIDR notation
  function validateCIDRInput(cidr: string): string | null {
    if (!cidr || cidr.trim().length === 0) {
      return 'Subnet cannot be empty';
    }

    const trimmed = cidr.trim();

    // Basic format check
    if (!trimmed.includes('/')) {
      return 'Invalid CIDR format. Use format like 192.168.1.0/24';
    }

    const [ip, prefix] = trimmed.split('/');

    // Check if we have both IP and prefix
    if (!ip || !prefix) {
      return 'Invalid CIDR format. Use format like 192.168.1.0/24';
    }

    const prefixNum = parseInt(prefix, 10);

    // Validate prefix length
    if (isNaN(prefixNum) || prefixNum < 0 || prefixNum > 32) {
      return 'Prefix length must be between 0 and 32';
    }

    // Validate IP address format
    const octets = ip.split('.');
    if (octets.length !== 4) {
      return 'Invalid IP address. Each octet must be 0-255';
    }

    // Validate IP address octets
    for (const octet of octets) {
      const num = parseInt(octet, 10);
      if (isNaN(num) || num < 0 || num > 255) {
        return 'Invalid IP address. Each octet must be 0-255';
      }
    }

    // Final security check using the utility
    if (!validateCIDR(trimmed)) {
      return 'Invalid CIDR format. Use format like 192.168.1.0/24';
    }

    return null;
  }

  function addSubnet() {
    const trimmed = newSubnet.trim();
    if (!trimmed) return;

    const validation = validateCIDRInput(trimmed);
    if (validation) {
      return; // Don't add invalid subnets
    }

    if (subnets.includes(trimmed)) {
      return; // Don't add duplicates
    }

    if (subnets.length >= maxItems) {
      return; // Don't exceed max items
    }

    const updated = [...subnets, trimmed];
    onUpdate(updated);
    newSubnet = '';
  }

  function removeSubnet(index: number) {
    const updated = subnets.filter((_, i) => i !== index);
    onUpdate(updated);

    // Clear error for removed item
    errors.deleteByIndex(index);
  }

  function updateSubnet(index: number, value: string) {
    const updated = [...subnets];
    const existingSubnet = safeArrayAccess(updated, index);
    if (existingSubnet !== undefined && index >= 0 && index < updated.length) {
      updated.splice(index, 1, value);
    }

    // Validate the updated subnet
    const validation = validateCIDRInput(value);
    if (validation) {
      errors.setByIndex(index, validation);
    } else {
      errors.deleteByIndex(index);
    }

    onUpdate(updated);
  }

  function handleKeyDown(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      addSubnet();
    }
  }

  function handleNewSubnetInput(event: Event) {
    const target = event.target as HTMLInputElement;
    newSubnet = target.value;
  }

  // Check if new subnet input is valid
  let newSubnetError = $derived.by(() => {
    if (!newSubnet.trim()) return null;
    return validateCIDRInput(newSubnet.trim());
  });

  let canAdd = $derived(
    newSubnet.trim().length > 0 &&
      !newSubnetError &&
      !subnets.includes(newSubnet.trim()) &&
      subnets.length < maxItems
  );
</script>

<div class={cn('form-control', className)} {...rest}>
  <label for={fieldId} class="label">
    <span class="label-text font-medium">
      {label}
    </span>
  </label>

  <!-- Add new subnet input -->
  <div class="flex gap-2 mb-3">
    <input
      id={fieldId}
      type="text"
      bind:value={newSubnet}
      {placeholder}
      {disabled}
      class={cn('input input-bordered flex-1', newSubnetError ? 'input-error' : '')}
      onkeydown={handleKeyDown}
      oninput={handleNewSubnetInput}
      aria-describedby={helpText ? `${fieldId}-help` : undefined}
    />
    <button
      type="button"
      class="btn btn-primary"
      onclick={addSubnet}
      disabled={disabled || !canAdd}
      aria-label="Add subnet"
    >
      {@html actionIcons.add}
      Add
    </button>
  </div>

  <!-- New subnet input error -->
  {#if newSubnetError}
    <div class="text-error text-sm mb-2">{newSubnetError}</div>
  {/if}

  <!-- Help text -->
  {#if helpText}
    <div id="{fieldId}-help" class="label">
      <span class="label-text-alt">{helpText}</span>
    </div>
  {/if}

  <!-- Subnet list -->
  {#if subnets.length > 0}
    <div class="space-y-2 mt-2">
      <div class="text-sm font-medium text-base-content/70">
        Allowed Subnets ({subnets.length}/{maxItems}):
      </div>

      {#each subnets as subnet, index}
        <div class="flex items-center gap-2 p-2 bg-base-200 rounded-lg">
          <input
            type="text"
            value={subnet}
            oninput={e => updateSubnet(index, (e.target as HTMLInputElement)?.value || '')}
            {disabled}
            class={cn(
              'input input-sm input-bordered flex-1',
              errors.getByIndex(index) ? 'input-error' : ''
            )}
          />
          <button
            type="button"
            class="btn btn-ghost btn-sm btn-square text-error"
            onclick={() => removeSubnet(index)}
            {disabled}
            aria-label="Remove subnet"
          >
            {@html navigationIcons.close}
          </button>
        </div>

        {#if errors.getByIndex(index)}
          <div class="text-error text-sm ml-2">{errors.getByIndex(index)}</div>
        {/if}
      {/each}
    </div>
  {:else}
    <div class="text-center py-4 text-base-content/60 bg-base-200 rounded-lg mt-2">
      <div class="text-sm">No subnets configured</div>
      <div class="text-xs">{emptyStateMessage}</div>
    </div>
  {/if}

  <!-- Max items warning -->
  {#if subnets.length >= maxItems}
    <div class="alert alert-warning mt-2">
      {@html alertIconsSvg.warning}
      <span>Maximum number of subnets ({maxItems}) reached.</span>
    </div>
  {/if}

  <!-- Main error display -->
  {#if error}
    <div class="label">
      <span class="label-text-alt text-error">{error}</span>
    </div>
  {/if}
</div>
