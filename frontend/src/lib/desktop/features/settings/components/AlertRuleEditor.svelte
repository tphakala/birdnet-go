<!--
  Alert Rule Editor Component

  Purpose: Modal for creating and editing alert rules with dynamic
  condition builder populated from the alerting schema.

  Props:
  - isOpen: boolean - controls modal visibility
  - rule: AlertRule | null - rule to edit, or null for new
  - schema: AlertSchema - schema for populating dropdowns
  - onSave: (rule) => void - called on save
  - onClose: () => void - called on cancel/close

  @component
-->
<script lang="ts">
  import Modal from '$lib/desktop/components/ui/Modal.svelte';
  import TextInput from '$lib/desktop/components/forms/TextInput.svelte';
  import Checkbox from '$lib/desktop/components/forms/Checkbox.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import { Plus, Trash2 } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { AlertRule, AlertSchema, ObjectTypeSchema } from '$lib/api/alerts';

  interface Props {
    isOpen: boolean;
    rule: AlertRule | null;
    schema: AlertSchema;
    onSave: (_data: Partial<AlertRule>) => void;
    onClose: () => void;
  }

  let { isOpen, rule, schema, onSave, onClose }: Props = $props();

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

  const newConditionId = () => crypto.randomUUID();

  interface EditorAction {
    target: string;
    template_title: string;
    template_message: string;
  }

  let conditions = $state<EditorCondition[]>([]);
  let actions = $state<EditorAction[]>([]);

  // Reset form when rule changes
  $effect(() => {
    if (isOpen) {
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

<Modal
  {isOpen}
  title={rule ? t('settings.alerts.editor.editTitle') : t('settings.alerts.editor.createTitle')}
  size="lg"
  {onClose}
>
  <div class="space-y-5">
    <!-- Name & Description -->
    <div class="space-y-3">
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
    </div>

    <!-- Trigger -->
    <div class="space-y-3">
      <h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">
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
        <h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">
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
                  class="block text-xs text-gray-500 dark:text-gray-400"
                >
                  {#if index === 0}{t('settings.alerts.editor.duration')}{/if}
                </label>
                <input
                  id="condition-duration-{index}"
                  type="number"
                  min="0"
                  class="w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
                  value={condition.duration_sec ?? 0}
                  onchange={e => {
                    condition.duration_sec = Number(e.currentTarget.value);
                  }}
                />
              </div>
            {/if}
            <button
              class="mb-0.5 rounded p-1.5 text-gray-400 hover:bg-red-50 hover:text-red-500 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              aria-label={t('settings.alerts.editor.removeCondition')}
              onclick={() => removeCondition(index)}
            >
              <Trash2 class="h-4 w-4" />
            </button>
          </div>
        {/each}
        <button
          class="flex items-center gap-1.5 text-sm text-blue-600 hover:text-blue-700 dark:text-blue-400 dark:hover:text-blue-300"
          onclick={addCondition}
        >
          <Plus class="h-4 w-4" />
          {t('settings.alerts.editor.addCondition')}
        </button>
      </div>
    {/if}

    <!-- Actions -->
    <div class="space-y-3">
      <h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">
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
      <h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">
        {t('settings.alerts.editor.optionsSection')}
      </h4>
      <div class="mt-2 w-32">
        <label for="cooldown-minutes" class="block text-xs text-gray-500 dark:text-gray-400">
          {t('settings.alerts.editor.cooldownMinutes')}
        </label>
        <input
          id="cooldown-minutes"
          type="number"
          class="w-full rounded-md border border-gray-300 px-2 py-1.5 text-sm dark:border-gray-600 dark:bg-gray-700 dark:text-gray-200"
          value={cooldownMin}
          min="0"
          onchange={e => {
            cooldownMin = Number(e.currentTarget.value);
          }}
        />
      </div>
    </div>
  </div>

  {#snippet footer()}
    <div class="flex justify-end gap-2">
      <button class="btn btn-ghost" onclick={onClose}>
        {t('common.buttons.cancel')}
      </button>
      <button class="btn btn-primary" disabled={!isValid} onclick={handleSave}>
        {rule ? t('common.buttons.save') : t('common.buttons.create')}
      </button>
    </div>
  {/snippet}
</Modal>
