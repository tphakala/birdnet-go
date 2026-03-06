<!--
  Alert Rule Editor Component

  Purpose: Inline card form for creating and editing alert rules with dynamic
  condition builder populated from the alerting schema. Compact 2-column layout
  with custom dropdowns for object type and event/metric selection.

  Props:
  - rule: AlertRule | null - rule to edit, or null for new
  - schema: AlertSchema - schema for populating dropdowns
  - onSave: (rule) => void - called on save
  - onClose: () => void - called on cancel/close
  - onDelete?: (rule) => void - called on delete (optional)

  @component
-->
<script lang="ts">
  import {
    Plus,
    Trash2,
    X,
    ChevronDown,
    Check,
    Bird,
    Activity,
    Radio,
    Cpu,
    Globe,
    Server,
    Zap,
    Gauge,
    Bell,
    Send,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import type { AlertRule, AlertSchema, ObjectTypeSchema } from '$lib/api/alerts';
  import {
    schemaObjectTypeLabel,
    schemaEventLabel,
    schemaMetricLabel,
    schemaPropertyLabel,
    schemaOperatorLabel,
  } from '$lib/utils/alertSchema';
  import { translateField } from '$lib/utils/notifications';

  interface Props {
    rule: AlertRule | null;
    schema: AlertSchema;
    onSave: (_data: Partial<AlertRule>) => void;
    onClose: () => void;
    onDelete?: (_rule: AlertRule) => void;
  }

  let { rule, schema, onSave, onClose, onDelete }: Props = $props();

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

  // Dropdown state
  let objDropOpen = $state(false);
  let eventDropOpen = $state(false);

  // Initialize form state from rule prop
  $effect(() => {
    if (rule) {
      name = translateField(rule.name_key, undefined, rule.name);
      description = translateField(rule.description_key, undefined, rule.description);
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
  let selectedObjectType = $derived<ObjectTypeSchema | undefined>(
    schema.objectTypes.find(ot => ot.name === objectType)
  );

  let hasEvents = $derived((selectedObjectType?.events?.length ?? 0) > 0);

  let hasMetrics = $derived((selectedObjectType?.metrics?.length ?? 0) > 0);

  let eventOptions = $derived(
    selectedObjectType?.events?.map(e => ({
      value: e.name,
      label: schemaEventLabel(e.name, e.label),
    })) ?? []
  );

  let metricOptions = $derived(
    selectedObjectType?.metrics?.map(m => ({
      value: m.name,
      label: `${schemaMetricLabel(m.name, m.label)} (${m.unit})`,
      unit: m.unit,
    })) ?? []
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

  let propertyOptions = $derived(
    availableProperties.map(p => ({ value: p.name, label: schemaPropertyLabel(p.name, p.label) }))
  );

  // Get operators for a given property
  function operatorsForProperty(propName: string) {
    const prop = availableProperties.find(p => p.name === propName);
    if (!prop) return [];
    return prop.operators.map(op => {
      const schemaOp = schema.operators.find(o => o.name === op);
      const fallback = schemaOp?.label ?? op;
      return { value: op, label: schemaOperatorLabel(op, fallback) };
    });
  }

  // Object type display helpers
  function objectTypeIcon(typeName: string) {
    const icons: Record<string, typeof Bird> = {
      detection: Bird,
      stream: Activity,
      device: Radio,
      system: Cpu,
      integration: Globe,
      application: Server,
    };
    return icons[typeName] ?? Cpu;
  }

  function objectTypeColor(typeName: string): { bg: string; text: string } {
    const colors: Record<string, { bg: string; text: string }> = {
      detection: { bg: 'bg-emerald-500/10', text: 'text-emerald-500' },
      stream: { bg: 'bg-blue-500/10', text: 'text-blue-500' },
      device: { bg: 'bg-violet-500/10', text: 'text-violet-500' },
      system: { bg: 'bg-red-500/10', text: 'text-red-500' },
      integration: { bg: 'bg-amber-500/10', text: 'text-amber-500' },
      application: { bg: 'bg-sky-500/10', text: 'text-sky-500' },
    };
    return colors[typeName] ?? { bg: 'bg-base-300', text: 'text-base-content' };
  }

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

    // Preserve translation keys only when the user has not changed the
    // built-in rule's name/description; clear them otherwise so the
    // custom text takes precedence.
    const translatedOriginalName = rule?.name_key
      ? translateField(rule.name_key, undefined, rule.name)
      : rule?.name;
    const translatedOriginalDesc = rule?.description_key
      ? translateField(rule.description_key, undefined, rule.description)
      : rule?.description;
    const nameKey = rule?.name_key && name.trim() === translatedOriginalName ? rule.name_key : '';
    const descKey =
      rule?.description_key && description.trim() === translatedOriginalDesc
        ? rule.description_key
        : '';

    onSave({
      id: rule?.id,
      name: name.trim(),
      description: description.trim(),
      name_key: nameKey || undefined,
      description_key: descKey || undefined,
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
  function handleObjectTypeChange(newType: string) {
    objectType = newType;
    objDropOpen = false;
    eventName = '';
    metricName = '';
    conditions = [];
    // Auto-select trigger type based on available triggers
    // Need to check against the new object type directly since derived hasn't updated yet
    const newOt = schema.objectTypes.find(ot => ot.name === newType);
    const newHasEvents = (newOt?.events?.length ?? 0) > 0;
    const newHasMetrics = (newOt?.metrics?.length ?? 0) > 0;
    if (newHasEvents && !newHasMetrics) triggerType = 'event';
    else if (newHasMetrics && !newHasEvents) triggerType = 'metric';
  }

  function handleTriggerTypeChange(newType: 'event' | 'metric') {
    triggerType = newType;
    eventName = '';
    metricName = '';
    conditions = [];
  }

  // Close dropdowns on click outside
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as HTMLElement;
    if (!target.closest('[data-dropdown]')) {
      objDropOpen = false;
      eventDropOpen = false;
    }
  }

  // Selected event/metric label for display
  let selectedTriggerLabel = $derived.by(() => {
    if (triggerType === 'event') {
      const ev = eventOptions.find(e => e.value === eventName);
      return ev?.label ?? '';
    }
    const mt = metricOptions.find(m => m.value === metricName);
    return mt?.label ?? '';
  });
</script>

<svelte:document onclick={handleClickOutside} />

<!-- Card container -->
<div class="rounded-lg bg-base-100 border border-primary overflow-hidden">
  <!-- Header bar -->
  <div class="px-5 py-3 border-b border-base-300 flex items-center justify-between">
    <h3 class="text-sm font-semibold text-base-content">
      {rule ? t('settings.alerts.editor.editTitle') : t('settings.alerts.editor.createTitle')}
    </h3>
    <button
      class="w-7 h-7 rounded-md flex items-center justify-center hover:bg-base-200 transition-colors cursor-pointer"
      aria-label={t('common.close')}
      onclick={onClose}
    >
      <X class="w-4 h-4 text-base-content/60" />
    </button>
  </div>

  <div class="p-5 space-y-4">
    <!-- Row 1: Name + Description -->
    <div class="grid grid-cols-2 gap-3">
      <div>
        <label for="rule-name" class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.name')}
        </label>
        <input
          id="rule-name"
          type="text"
          bind:value={name}
          placeholder={t('settings.alerts.editor.namePlaceholder')}
          class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content placeholder:text-base-content/40 outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary transition-colors"
        />
      </div>
      <div>
        <label for="rule-desc" class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.description')}
        </label>
        <input
          id="rule-desc"
          type="text"
          bind:value={description}
          placeholder={t('settings.alerts.editor.descriptionPlaceholder')}
          class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content placeholder:text-base-content/40 outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary transition-colors"
        />
      </div>
    </div>

    <!-- Row 2: Object Type dropdown + Trigger type toggle -->
    <div class="grid grid-cols-2 gap-3">
      <!-- Object Type custom dropdown -->
      <div class="relative" data-dropdown>
        <span class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.objectType')}
        </span>
        <button
          type="button"
          aria-haspopup="listbox"
          aria-expanded={objDropOpen}
          aria-label={t('settings.alerts.editor.objectType')}
          class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border text-left flex items-center gap-2 cursor-pointer transition-all {objDropOpen
            ? 'ring-2 ring-primary/20 border-primary'
            : 'border-base-300'}"
          onclick={() => {
            objDropOpen = !objDropOpen;
            eventDropOpen = false;
          }}
        >
          {#if selectedObjectType}
            {@const OIcon = objectTypeIcon(objectType)}
            {@const oColor = objectTypeColor(objectType)}
            <div
              class="w-5 h-5 rounded-md flex items-center justify-center flex-shrink-0 {oColor.bg}"
            >
              <OIcon class="w-3 h-3 {oColor.text}" />
            </div>
            <div class="flex-1 min-w-0">
              <span class="text-base-content truncate block">
                {schemaObjectTypeLabel(selectedObjectType.name, selectedObjectType.label)}
              </span>
              <span class="text-[11px] text-base-content/40">
                {selectedObjectType.events?.length ?? 0}
                {t('settings.alerts.editor.eventsCount')} &middot; {selectedObjectType.metrics
                  ?.length ?? 0}
                {t('settings.alerts.editor.metricsCount')}
              </span>
            </div>
          {/if}
          <ChevronDown
            class="w-3.5 h-3.5 flex-shrink-0 text-base-content/40 transition-transform {objDropOpen
              ? 'rotate-180'
              : ''}"
          />
        </button>
        {#if objDropOpen}
          <div
            role="listbox"
            class="absolute z-50 top-full left-0 right-0 mt-1 bg-base-100 border border-base-300 shadow-lg rounded-lg overflow-hidden"
          >
            {#each schema.objectTypes as ot (ot.name)}
              {@const OIcon = objectTypeIcon(ot.name)}
              {@const oColor = objectTypeColor(ot.name)}
              <button
                type="button"
                role="option"
                aria-selected={objectType === ot.name}
                class="w-full flex items-center gap-2.5 px-3 py-2.5 text-left transition-colors cursor-pointer hover:bg-base-200 {objectType ===
                ot.name
                  ? 'bg-primary/5'
                  : ''}"
                onclick={() => handleObjectTypeChange(ot.name)}
              >
                <div
                  class="w-7 h-7 rounded-lg flex items-center justify-center flex-shrink-0 {oColor.bg}"
                >
                  <OIcon class="w-3.5 h-3.5 {oColor.text}" />
                </div>
                <div class="flex-1 min-w-0">
                  <div class="text-sm font-medium text-base-content">
                    {schemaObjectTypeLabel(ot.name, ot.label)}
                  </div>
                  <div class="text-[11px] text-base-content/40">
                    {ot.events?.length ?? 0}
                    {t('settings.alerts.editor.eventsCount')} &middot; {ot.metrics?.length ?? 0}
                    {t('settings.alerts.editor.metricsCount')}
                  </div>
                </div>
                {#if objectType === ot.name}
                  <Check class="w-3.5 h-3.5 text-primary flex-shrink-0" />
                {/if}
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Trigger type toggle -->
      <div>
        <span class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.triggerSection')}
        </span>
        {#if hasEvents && hasMetrics}
          <div class="flex gap-2">
            <button
              type="button"
              class="flex-1 px-3 py-2 rounded-lg text-sm font-medium border transition-all cursor-pointer {triggerType ===
              'event'
                ? 'ring-2 ring-primary/20 border-primary bg-primary/5'
                : 'bg-base-200 border-base-300'}"
              onclick={() => handleTriggerTypeChange('event')}
            >
              <span class="flex items-center gap-1.5 justify-center text-base-content">
                <Zap class="w-3.5 h-3.5" />
                {t('settings.alerts.editor.triggerEvent')}
              </span>
            </button>
            <button
              type="button"
              class="flex-1 px-3 py-2 rounded-lg text-sm font-medium border transition-all cursor-pointer {triggerType ===
              'metric'
                ? 'ring-2 ring-primary/20 border-primary bg-primary/5'
                : 'bg-base-200 border-base-300'}"
              onclick={() => handleTriggerTypeChange('metric')}
            >
              <span class="flex items-center gap-1.5 justify-center text-base-content">
                <Gauge class="w-3.5 h-3.5" />
                {t('settings.alerts.editor.triggerMetric')}
              </span>
            </button>
          </div>
        {:else}
          <div
            class="px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content"
          >
            <span class="flex items-center gap-1.5">
              {#if hasEvents}
                <Zap class="w-3.5 h-3.5" />
                {t('settings.alerts.editor.triggerEvent')}
              {:else}
                <Gauge class="w-3.5 h-3.5" />
                {t('settings.alerts.editor.triggerMetric')}
              {/if}
            </span>
          </div>
        {/if}
      </div>
    </div>

    <!-- Row 3: Event/Metric selector (full width) -->
    <div class="relative" data-dropdown>
      <span class="block text-xs font-medium text-base-content/60 mb-1">
        {triggerType === 'event'
          ? t('settings.alerts.editor.event')
          : t('settings.alerts.editor.metric')}
      </span>
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={eventDropOpen}
        aria-label={triggerType === 'event'
          ? t('settings.alerts.editor.event')
          : t('settings.alerts.editor.metric')}
        class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border text-left flex items-center gap-2 cursor-pointer transition-all {eventDropOpen
          ? 'ring-2 ring-primary/20 border-primary'
          : 'border-base-300'}"
        onclick={() => {
          eventDropOpen = !eventDropOpen;
          objDropOpen = false;
        }}
      >
        <span class="flex-1 truncate text-base-content">
          {selectedTriggerLabel ||
            (triggerType === 'event'
              ? t('settings.alerts.editor.event')
              : t('settings.alerts.editor.metric'))}
        </span>
        <ChevronDown
          class="w-3.5 h-3.5 flex-shrink-0 text-base-content/40 transition-transform {eventDropOpen
            ? 'rotate-180'
            : ''}"
        />
      </button>
      {#if eventDropOpen}
        {@const items = triggerType === 'event' ? eventOptions : metricOptions}
        <div
          role="listbox"
          class="absolute z-50 top-full left-0 right-0 mt-1 bg-base-100 border border-base-300 shadow-lg rounded-lg overflow-hidden max-h-60 overflow-y-auto"
        >
          {#each items as item (item.value)}
            {@const isSelected = (triggerType === 'event' ? eventName : metricName) === item.value}
            <button
              type="button"
              role="option"
              aria-selected={isSelected}
              class="w-full flex items-center gap-2.5 px-3 py-2.5 text-left transition-colors cursor-pointer hover:bg-base-200 {isSelected
                ? 'bg-primary/5'
                : ''}"
              onclick={() => {
                if (triggerType === 'event') eventName = item.value;
                else metricName = item.value;
                conditions = [];
                eventDropOpen = false;
              }}
            >
              <div class="flex-1 min-w-0">
                <div class="text-sm font-medium text-base-content">{item.label}</div>
                <div class="text-[11px] font-mono text-base-content/40">
                  {item.value}
                </div>
              </div>
              {#if isSelected}
                <Check class="w-3.5 h-3.5 text-primary flex-shrink-0" />
              {/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Row 4: Conditions (full width) -->
    <div>
      <div class="flex items-center justify-between mb-1.5">
        <span class="text-xs font-medium text-base-content/60">
          {t('settings.alerts.editor.conditionsSection')}
          {#if conditions.length > 0}
            ({conditions.length})
          {/if}
        </span>
        {#if availableProperties.length > 0}
          <button
            type="button"
            class="flex items-center gap-1 text-[11px] font-medium text-primary hover:bg-primary/10 px-2 py-1 rounded-md transition-colors cursor-pointer"
            onclick={addCondition}
          >
            <Plus class="w-3 h-3" />
            {t('settings.alerts.editor.addCondition')}
          </button>
        {/if}
      </div>
      {#if conditions.length === 0}
        <div class="px-3 py-2.5 rounded-lg text-xs bg-base-200 text-base-content/40">
          {triggerType === 'event'
            ? t('settings.alerts.editor.noConditionsEvent')
            : t('settings.alerts.editor.noConditionsMetric')}
        </div>
      {:else}
        <div class="space-y-2">
          {#each conditions as condition, index (condition.id)}
            <div
              class="flex items-center gap-2 p-2.5 rounded-lg border border-base-300 bg-base-200"
            >
              <!-- Property -->
              <select
                bind:value={condition.property}
                aria-label={t('settings.alerts.editor.property')}
                class="px-2 py-1.5 rounded-md text-xs border border-base-300 bg-base-100 text-base-content cursor-pointer outline-none focus:ring-1 focus:ring-primary/30"
              >
                {#each propertyOptions as prop}
                  <option value={prop.value}>{prop.label}</option>
                {/each}
              </select>
              <!-- Operator -->
              <select
                bind:value={condition.operator}
                aria-label={t('settings.alerts.editor.operator')}
                class="px-2 py-1.5 rounded-md text-xs border border-base-300 bg-base-100 text-base-content font-mono cursor-pointer outline-none focus:ring-1 focus:ring-primary/30"
              >
                {#each operatorsForProperty(condition.property ?? '') as op}
                  <option value={op.value}>{op.label}</option>
                {/each}
              </select>
              <!-- Value -->
              <input
                type="text"
                bind:value={condition.value}
                aria-label={t('settings.alerts.editor.value')}
                placeholder={t('settings.alerts.editor.valuePlaceholder')}
                class="flex-1 px-2 py-1.5 rounded-md text-xs border border-base-300 bg-base-100 text-base-content outline-none focus:ring-1 focus:ring-primary/30 tabular-nums placeholder:text-base-content/40"
              />
              <!-- Duration (metric only) -->
              {#if triggerType === 'metric'}
                <div class="flex items-center gap-1">
                  <span class="text-[10px] text-base-content/40"
                    >{t('settings.alerts.editor.durationFor')}</span
                  >
                  <input
                    type="number"
                    min="0"
                    aria-label={t('settings.alerts.editor.duration')}
                    class="w-16 px-2 py-1.5 rounded-md text-xs border border-base-300 bg-base-100 text-base-content outline-none tabular-nums focus:ring-1 focus:ring-primary/30"
                    value={condition.duration_sec ?? 0}
                    onchange={e => {
                      condition.duration_sec = Number(e.currentTarget.value);
                    }}
                  />
                  <span class="text-[10px] text-base-content/40"
                    >{t('settings.alerts.editor.durationSec')}</span
                  >
                </div>
              {/if}
              <!-- Remove -->
              <button
                type="button"
                class="w-6 h-6 rounded-md flex items-center justify-center hover:bg-error/10 transition-colors cursor-pointer"
                aria-label={t('settings.alerts.editor.removeCondition')}
                onclick={() => removeCondition(index)}
              >
                <X class="w-3.5 h-3.5 text-error" />
              </button>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Row 5: Actions + Cooldown -->
    <div class="grid grid-cols-2 gap-3">
      <!-- Actions toggle buttons -->
      <div>
        <span class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.actionsSection')}
        </span>
        <div class="flex gap-2">
          <button
            type="button"
            class="flex-1 flex items-center gap-2 px-3 py-2 rounded-lg text-xs font-medium border transition-all cursor-pointer {isActionSelected(
              'bell'
            )
              ? 'ring-2 ring-primary/20 border-primary bg-primary/5'
              : 'bg-base-200 border-base-300 opacity-50'}"
            onclick={() => toggleAction('bell')}
          >
            <Bell class="w-3.5 h-3.5 text-base-content" />
            <span class="text-base-content">{t('settings.alerts.editor.actionBell')}</span>
            {#if isActionSelected('bell')}
              <Check class="w-3 h-3 text-primary ml-auto" />
            {/if}
          </button>
          <button
            type="button"
            class="flex-1 flex items-center gap-2 px-3 py-2 rounded-lg text-xs font-medium border transition-all cursor-pointer {isActionSelected(
              'push'
            )
              ? 'ring-2 ring-primary/20 border-primary bg-primary/5'
              : 'bg-base-200 border-base-300 opacity-50'}"
            onclick={() => toggleAction('push')}
          >
            <Send class="w-3.5 h-3.5 text-base-content" />
            <span class="text-base-content">{t('settings.alerts.editor.actionPush')}</span>
            {#if isActionSelected('push')}
              <Check class="w-3 h-3 text-primary ml-auto" />
            {/if}
          </button>
        </div>
      </div>

      <!-- Cooldown -->
      <div>
        <label for="rule-cooldown" class="block text-xs font-medium text-base-content/60 mb-1">
          {t('settings.alerts.editor.cooldownMinutes')}
        </label>
        <input
          id="rule-cooldown"
          type="number"
          min="0"
          class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary transition-colors tabular-nums"
          value={cooldownMin}
          onchange={e => {
            cooldownMin = Number(e.currentTarget.value);
          }}
        />
      </div>
    </div>

    <!-- Action template fields (shown below when actions are selected) -->
    {#each ['bell', 'push'] as target (target)}
      {#if isActionSelected(target)}
        {@const action = actions.find(a => a.target === target)}
        {#if action}
          <div class="grid grid-cols-2 gap-3 pl-1 border-l-2 border-primary/20 ml-1">
            <div>
              <label
                for="template-title-{target}"
                class="block text-xs font-medium text-base-content/60 mb-1"
              >
                {t('settings.alerts.editor.templateTitle')} ({target === 'bell'
                  ? t('settings.alerts.editor.actionBell')
                  : t('settings.alerts.editor.actionPush')})
              </label>
              <input
                id="template-title-{target}"
                type="text"
                bind:value={action.template_title}
                placeholder={t('settings.alerts.editor.templateTitlePlaceholder')}
                class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content placeholder:text-base-content/40 outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary transition-colors"
              />
            </div>
            <div>
              <label
                for="template-msg-{target}"
                class="block text-xs font-medium text-base-content/60 mb-1"
              >
                {t('settings.alerts.editor.templateMessage')}
              </label>
              <input
                id="template-msg-{target}"
                type="text"
                bind:value={action.template_message}
                placeholder={t('settings.alerts.editor.templateMessagePlaceholder')}
                class="w-full px-3 py-2 rounded-lg text-sm bg-base-200 border border-base-300 text-base-content placeholder:text-base-content/40 outline-none focus:ring-2 focus:ring-primary/20 focus:border-primary transition-colors"
              />
            </div>
          </div>
        {/if}
      {/if}
    {/each}

    <!-- Row 6: Footer - Delete (left) + Cancel/Save (right) -->
    <div class="flex items-center justify-between pt-2">
      <div>
        {#if rule && !rule.built_in && onDelete}
          <button
            type="button"
            class="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-medium text-error hover:bg-error/10 transition-colors cursor-pointer"
            onclick={() => onDelete?.(rule)}
          >
            <Trash2 class="w-3.5 h-3.5" />
            {t('settings.alerts.actionLabels.delete')}
          </button>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        <button
          type="button"
          class="px-4 py-1.5 rounded-lg text-xs font-medium text-base-content/60 hover:bg-base-200 transition-colors cursor-pointer"
          onclick={onClose}
        >
          {t('common.buttons.cancel')}
        </button>
        <button
          type="button"
          onclick={handleSave}
          disabled={!isValid}
          class="px-4 py-1.5 rounded-lg text-xs font-medium bg-primary text-primary-content hover:opacity-90 transition-colors cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
        >
          {rule ? t('common.buttons.save') : t('common.buttons.create')}
        </button>
      </div>
    </div>
  </div>
</div>
