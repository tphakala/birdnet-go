<!--
  Alert Rule Editor Component

  Purpose: Inline form for creating and editing alert rules with dynamic
  condition builder populated from the alerting schema. Renders as an
  inline card matching the notification provider form pattern.

  Props:
  - rule: AlertRule | null - rule to edit, or null for new
  - schema: AlertSchema - schema for populating dropdowns
  - onSave: (rule) => void - called on save
  - onClose: () => void - called on cancel/close

  @component
-->
<script lang="ts">
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import { Plus, Trash2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { AlertRule, AlertSchema, ObjectTypeSchema } from '$lib/api/alerts';

  interface Props {
    rule: AlertRule | null;
    schema: AlertSchema;
    onSave: (_data: Partial<AlertRule>) => void;
    onClose: () => void;
  }

  let { rule, schema, onSave, onClose }: Props = $props();

  // Form state
  let name = $state('');
  let description = $state('');
  let enabled = $state(true);
  let objectType = $state('');
  let triggerType = $state<'event' | 'metric'>('event');
  let eventName = $state('');
  let metricName = $state('');
  let cooldownMin = $state(5);
  interface EditorCondition {
    id: string;
    property: string;
    operator: string;
    value: string;
    duration_sec: number;
  }

  const newConditionId = () =>
    crypto?.randomUUID?.() ?? Math.random().toString(36).substring(2, 11);

  interface EditorAction {
    target: string;
    template_title: string;
    template_message: string;
  }

  let conditions = $state<EditorCondition[]>([]);
  let actions = $state<EditorAction[]>([]);

  // Initialize form state from rule prop
  $effect(() => {
    if (rule) {
      name = rule.name;
      description = rule.description;
      enabled = rule.enabled;
      objectType = rule.object_type;
      triggerType = (rule.trigger_type as 'event' | 'metric') || 'event';
      eventName = rule.event_name;
      metricName = rule.metric_name;
      cooldownMin = Math.floor(rule.cooldown_sec / 60);
      conditions =
        rule.conditions?.map(c => ({
          id: newConditionId(),
          property: c.property,
          operator: c.operator,
          value: c.value,
          duration_sec: c.duration_sec,
        })) ?? [];
      actions =
        rule.actions?.map(a => ({
          target: a.target,
          template_title: a.template_title,
          template_message: a.template_message,
        })) ?? [];
    } else {
      name = '';
      description = '';
      enabled = true;
      objectType = schema.objectTypes[0]?.name ?? '';
      triggerType = 'event';
      eventName = '';
      metricName = '';
      cooldownMin = 5;
      conditions = [];
      actions = [{ target: 'bell', template_title: '', template_message: '' }];
    }
  });

  // Schema-driven options
  let objectTypeOptions = $derived<SelectOption[]>(
    schema.objectTypes.map(ot => ({ value: ot.name, label: ot.label }))
  );

  let selectedObjectType = $derived<ObjectTypeSchema | undefined>(
    schema.objectTypes.find(ot => ot.name === objectType)
  );

  let hasEvents = $derived((selectedObjectType?.events?.length ?? 0) > 0);

  let hasMetrics = $derived((selectedObjectType?.metrics?.length ?? 0) > 0);

  let triggerTypeOptions = $derived.by(() => {
    const opts: SelectOption[] = [];
    if (hasEvents) opts.push({ value: 'event', label: t('settings.alerts.editor.triggerEvent') });
    if (hasMetrics)
      opts.push({ value: 'metric', label: t('settings.alerts.editor.triggerMetric') });
    return opts;
  });

  let eventOptions = $derived<SelectOption[]>(
    selectedObjectType?.events?.map(e => ({ value: e.name, label: e.label })) ?? []
  );

  let metricOptions = $derived<SelectOption[]>(
    selectedObjectType?.metrics?.map(m => ({ value: m.name, label: `${m.label} (${m.unit})` })) ??
      []
  );

  // Get available properties for current trigger
  let availableProperties = $derived.by(
    (): { name: string; label: string; type: string; operators: string[] }[] => {
      if (triggerType === 'event' && eventName) {
        const event = selectedObjectType?.events?.find(e => e.name === eventName);
        return event?.properties ?? [];
      }
      if (triggerType === 'metric' && metricName) {
        const metric = selectedObjectType?.metrics?.find(m => m.name === metricName);
        return metric?.properties ?? [];
      }
      return [];
    }
  );

  let propertyOptions = $derived<SelectOption[]>(
    availableProperties.map(p => ({ value: p.name, label: p.label }))
  );

  // Get operators for a given property
  function operatorsForProperty(propName: string): SelectOption[] {
    const prop = availableProperties.find(p => p.name === propName);
    if (!prop) return [];
    return prop.operators.map(op => {
      const schemaOp = schema.operators.find(o => o.name === op);
      return { value: op, label: schemaOp?.label ?? op };
    });
  }

  // Action target options
  let actionTargets = $derived<SelectOption[]>([
    { value: 'bell', label: t('settings.alerts.editor.actionBell') },
    { value: 'push', label: t('settings.alerts.editor.actionPush') },
  ]);

  // Condition management
  function addCondition() {
    conditions = [
      ...conditions,
      {
        id: newConditionId(),
        property: availableProperties[0]?.name ?? '',
        operator: '',
        value: '',
        duration_sec: 0,
      },
    ];
  }

  function removeCondition(index: number) {
    conditions = conditions.filter((_, i) => i !== index);
  }

  // Action management
  function toggleAction(target: string) {
    const exists = actions.some(a => a.target === target);
    if (exists) {
      actions = actions.filter(a => a.target !== target);
    } else {
      actions = [...actions, { target, template_title: '', template_message: '' }];
    }
  }

  function isActionSelected(target: string): boolean {
    return actions.some(a => a.target === target);
  }

  // Validation
  let isValid = $derived(
    name.trim() !== '' &&
      objectType !== '' &&
      ((triggerType === 'event' && eventName !== '') ||
        (triggerType === 'metric' && metricName !== '')) &&
      actions.length > 0
  );

  function handleSave() {
    if (!isValid) return;
    onSave({
      id: rule?.id,
      name: name.trim(),
      description: description.trim(),
      enabled,
      object_type: objectType,
      trigger_type: triggerType,
      event_name: triggerType === 'event' ? eventName : '',
      metric_name: triggerType === 'metric' ? metricName : '',
      cooldown_sec: cooldownMin * 60,
      conditions: conditions.map((c, i) => ({
        id: 0,
        rule_id: 0,
        property: c.property,
        operator: c.operator,
        value: c.value,
        duration_sec: c.duration_sec,
        sort_order: i,
      })),
      actions: actions.map((a, i) => ({
        id: 0,
        rule_id: 0,
        target: a.target,
        template_title: a.template_title,
        template_message: a.template_message,
        sort_order: i,
      })),
    });
  }

  // Reset trigger-specific fields when object type changes
  function handleObjectTypeChange() {
    eventName = '';
    metricName = '';
    conditions = [];
    // Auto-select trigger type based on available triggers
    if (hasEvents && !hasMetrics) triggerType = 'event';
    else if (hasMetrics && !hasEvents) triggerType = 'metric';
  }

  function handleTriggerTypeChange() {
    eventName = '';
    metricName = '';
    conditions = [];
  }
</script>

<div class="rounded-lg bg-[var(--color-base-200)] border border-[var(--color-primary)]">
  <div class="p-6">
    <h3 class="text-base font-semibold mb-4">
      {rule ? t('settings.alerts.editor.editTitle') : t('settings.alerts.editor.createTitle')}
    </h3>

    <div class="space-y-4">
      <!-- Name & Description -->
      <TextInput
        label={t('settings.alerts.editor.name')}
        bind:value={name}
        placeholder={t('settings.alerts.editor.namePlaceholder')}
      />
      <TextInput
        label={t('settings.alerts.editor.description')}
        bind:value={description}
        placeholder={t('settings.alerts.editor.descriptionPlaceholder')}
      />
      <Checkbox label={t('settings.alerts.editor.enabled')} bind:checked={enabled} />

      <!-- Trigger -->
      <div class="space-y-3">
        <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
          {t('settings.alerts.editor.triggerSection')}
        </h4>
        <SelectDropdown
          options={objectTypeOptions}
          bind:value={objectType}
          label={t('settings.alerts.editor.objectType')}
          onChange={handleObjectTypeChange}
        />
        {#if hasEvents && hasMetrics}
          <SelectDropdown
            options={triggerTypeOptions}
            bind:value={triggerType}
            label={t('settings.alerts.editor.triggerType')}
            onChange={handleTriggerTypeChange}
          />
        {/if}
        {#if triggerType === 'event' && hasEvents}
          <SelectDropdown
            options={eventOptions}
            bind:value={eventName}
            label={t('settings.alerts.editor.event')}
          />
        {:else if triggerType === 'metric' && hasMetrics}
          <SelectDropdown
            options={metricOptions}
            bind:value={metricName}
            label={t('settings.alerts.editor.metric')}
          />
        {/if}
      </div>

      <!-- Conditions -->
      {#if availableProperties.length > 0}
        <div class="space-y-3">
          <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
            {t('settings.alerts.editor.conditionsSection')}
          </h4>
          {#each conditions as condition, index (condition.id)}
            <div class="flex items-end gap-2">
              <div class="flex-1">
                <SelectDropdown
                  options={propertyOptions}
                  bind:value={condition.property}
                  label={index === 0 ? t('settings.alerts.editor.property') : ''}
                />
              </div>
              <div class="w-36">
                <SelectDropdown
                  options={operatorsForProperty(condition.property ?? '')}
                  bind:value={condition.operator}
                  label={index === 0 ? t('settings.alerts.editor.operator') : ''}
                />
              </div>
              <div class="flex-1">
                <TextInput
                  bind:value={condition.value}
                  label={index === 0 ? t('settings.alerts.editor.value') : ''}
                  placeholder={t('settings.alerts.editor.valuePlaceholder')}
                />
              </div>
              {#if triggerType === 'metric'}
                <div class="w-24">
                  <label
                    for="condition-duration-{index}"
                    class="block text-xs text-[var(--color-base-content)] opacity-60"
                  >
                    {#if index === 0}{t('settings.alerts.editor.duration')}{/if}
                  </label>
                  <input
                    id="condition-duration-{index}"
                    type="number"
                    min="0"
                    class="w-full h-10 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
                    value={condition.duration_sec ?? 0}
                    onchange={e => {
                      condition.duration_sec = Number(e.currentTarget.value);
                    }}
                  />
                </div>
              {/if}
              <button
                class="mb-0.5 rounded p-1.5 text-[var(--color-base-content)] opacity-60 hover:bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] hover:text-[var(--color-error)] hover:opacity-100 transition-colors"
                aria-label={t('settings.alerts.editor.removeCondition')}
                onclick={() => removeCondition(index)}
              >
                <Trash2 class="size-4" />
              </button>
            </div>
          {/each}
          <button
            class="flex items-center gap-1.5 text-sm text-[var(--color-primary)] hover:opacity-80 transition-opacity"
            onclick={addCondition}
          >
            <Plus class="size-4" />
            {t('settings.alerts.editor.addCondition')}
          </button>
        </div>
      {/if}

      <!-- Actions -->
      <div class="space-y-3">
        <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
          {t('settings.alerts.editor.actionsSection')}
        </h4>
        {#each actionTargets as target (target.value)}
          <div>
            <Checkbox
              label={target.label}
              checked={isActionSelected(target.value)}
              onchange={() => toggleAction(target.value)}
            />
            {#if isActionSelected(target.value)}
              {@const action = actions.find(a => a.target === target.value)}
              {#if action}
                <div class="ml-6 mt-2 space-y-2">
                  <TextInput
                    label={t('settings.alerts.editor.templateTitle')}
                    bind:value={action.template_title}
                    placeholder={t('settings.alerts.editor.templateTitlePlaceholder')}
                  />
                  <TextInput
                    label={t('settings.alerts.editor.templateMessage')}
                    bind:value={action.template_message}
                    placeholder={t('settings.alerts.editor.templateMessagePlaceholder')}
                  />
                </div>
              {/if}
            {/if}
          </div>
        {/each}
      </div>

      <!-- Cooldown -->
      <div>
        <h4 class="text-sm font-semibold text-[var(--color-base-content)]">
          {t('settings.alerts.editor.optionsSection')}
        </h4>
        <div class="mt-2 w-32">
          <label
            for="cooldown-minutes"
            class="block text-xs text-[var(--color-base-content)] opacity-60"
          >
            {t('settings.alerts.editor.cooldownMinutes')}
          </label>
          <input
            id="cooldown-minutes"
            type="number"
            class="w-full h-10 px-3 text-sm bg-[var(--color-base-100)] border border-[var(--border-200)] rounded-lg focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)] focus:border-transparent transition-colors"
            value={cooldownMin}
            min="0"
            onchange={e => {
              cooldownMin = Number(e.currentTarget.value);
            }}
          />
        </div>
      </div>

      <!-- Form Actions -->
      <div class="flex gap-2 justify-end">
        <button
          onclick={onClose}
          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-transparent hover:bg-[color-mix(in_srgb,var(--color-base-content)_5%,transparent)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-base-content)] focus-visible:ring-offset-2 transition-colors"
        >
          {t('common.buttons.cancel')}
        </button>
        <button
          onclick={handleSave}
          disabled={!isValid}
          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-primary)] text-[var(--color-primary-content)] hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] focus-visible:ring-offset-2 transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {rule ? t('common.buttons.save') : t('common.buttons.create')}
        </button>
      </div>
    </div>
  </div>
</div>
