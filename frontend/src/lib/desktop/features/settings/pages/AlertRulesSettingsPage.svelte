<!--
  Alert Rules Settings Page Component

  Purpose: Configure alert rules and view alert firing history for BirdNET-Go.
  Rules match events (stream disconnected, new species) or metrics (CPU, memory, disk)
  against conditions and dispatch actions (bell notification, push notification).

  Features:
  - List, filter, toggle, delete alert rules
  - Per-rule cards with trigger, conditions, actions summary
  - Alert history with recent entries
  - Reset to defaults

  Props: None - This is a page component

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import SettingsSection from '$lib/desktop/features/settings/components/SettingsSection.svelte';
  import SettingsTabs from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import type { TabDefinition } from '$lib/desktop/features/settings/components/SettingsTabs.svelte';
  import SettingsButton from '$lib/desktop/features/settings/components/SettingsButton.svelte';
  import SelectDropdown from '$lib/desktop/components/forms/SelectDropdown.svelte';
  import type { SelectOption } from '$lib/desktop/components/forms/SelectDropdown.types';
  import {
    Bell,
    History,
    Plus,
    Pencil,
    Trash2,
    ToggleLeft,
    ToggleRight,
    Play,
    RotateCcw,
    CircleCheck,
    XCircle,
    Info,
    Shield,
    Download,
    Upload,
  } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { loggers } from '$lib/utils/logger';
  import { ApiError } from '$lib/utils/api';
  import {
    fetchAlertRules,
    createAlertRule,
    updateAlertRule,
    toggleAlertRule,
    deleteAlertRule,
    testAlertRule,
    resetAlertDefaults,
    fetchAlertHistory,
    clearAlertHistory,
    fetchAlertSchema,
    exportAlertRules,
    importAlertRules,
  } from '$lib/api/alerts';
  import type { AlertRule, AlertHistory as AlertHistoryType, AlertSchema } from '$lib/api/alerts';
  import AlertRuleEditor from '$lib/desktop/features/settings/components/AlertRuleEditor.svelte';
  import { formatLocalDateTime } from '$lib/utils/date';

  const logger = loggers.settings;

  const SECONDS_PER_MINUTE = 60;
  const STATUS_DISMISS_MS = 3000;
  const HISTORY_FETCH_LIMIT = 50;

  // Tab state
  let activeTab = $state('rules');

  // Rules state
  let rules = $state<AlertRule[]>([]);
  let loadingRules = $state(false);
  let schema = $state<AlertSchema | null>(null);

  // Filter state
  let filterObjectType = $state('');
  let filterEnabled = $state('');

  // History state
  let history = $state<AlertHistoryType[]>([]);
  let historyTotal = $state(0);
  let loadingHistory = $state(false);

  // Status message
  let statusMessage = $state('');
  let statusType = $state<'info' | 'success' | 'error'>('info');

  // Busy states
  let togglingId = $state<number | null>(null);
  let deletingId = $state<number | null>(null);
  let testingId = $state<number | null>(null);
  let resetting = $state(false);
  let clearingHistory = $state(false);
  let exporting = $state(false);
  let importing = $state(false);

  // Editor state
  let editorOpen = $state(false);
  let editingRule = $state<AlertRule | null>(null);

  // Schema-based filter options
  let objectTypeOptions = $derived<SelectOption[]>([
    { value: '', label: t('settings.alerts.filters.allTypes') },
    ...(schema?.objectTypes.map(ot => ({ value: ot.name, label: ot.label })) ?? []),
  ]);

  let enabledOptions = $derived<SelectOption[]>([
    { value: '', label: t('settings.alerts.filters.allStates') },
    { value: 'true', label: t('settings.alerts.filters.enabled') },
    { value: 'false', label: t('settings.alerts.filters.disabled') },
  ]);

  // Filtered rules
  let filteredRules = $derived.by(() => {
    let result = rules;
    if (filterObjectType) {
      result = result.filter(r => r.object_type === filterObjectType);
    }
    if (filterEnabled === 'true') {
      result = result.filter(r => r.enabled);
    } else if (filterEnabled === 'false') {
      result = result.filter(r => !r.enabled);
    }
    return result;
  });

  // Tab definitions
  let tabs = $derived<TabDefinition[]>([
    {
      id: 'rules',
      label: t('settings.alerts.tabs.rules'),
      icon: Bell,
      content: rulesTabContent,
      hasChanges: false,
    },
    {
      id: 'history',
      label: t('settings.alerts.tabs.history'),
      icon: History,
      content: historyTabContent,
      hasChanges: false,
    },
  ]);

  // Helper: get schema label for object type
  function objectTypeLabel(name: string): string {
    return schema?.objectTypes.find(ot => ot.name === name)?.label ?? name;
  }

  // Helper: get event/metric label
  function triggerLabel(rule: AlertRule): string {
    if (rule.trigger_type === 'event') {
      const ot = schema?.objectTypes.find(o => o.name === rule.object_type);
      return ot?.events?.find(e => e.name === rule.event_name)?.label ?? rule.event_name;
    }
    if (rule.trigger_type === 'metric') {
      const ot = schema?.objectTypes.find(o => o.name === rule.object_type);
      return ot?.metrics?.find(m => m.name === rule.metric_name)?.label ?? rule.metric_name;
    }
    return rule.event_name || rule.metric_name;
  }

  // Helper: conditions summary
  function conditionsSummary(rule: AlertRule): string {
    if (!rule.conditions || rule.conditions.length === 0) return t('settings.alerts.noConditions');
    return rule.conditions.map(c => `${c.property} ${c.operator} ${c.value}`).join(', ');
  }

  // Helper: actions summary
  function actionsSummary(rule: AlertRule): string {
    if (!rule.actions || rule.actions.length === 0) return t('settings.alerts.noActions');
    return rule.actions.map(a => a.target).join(', ');
  }

  // Helper: format cooldown
  function formatCooldown(seconds: number): string {
    if (seconds < SECONDS_PER_MINUTE) return `${seconds}s`;
    const minutes = Math.floor(seconds / SECONDS_PER_MINUTE);
    return `${minutes}m`;
  }

  function showStatus(msg: string, type: 'info' | 'success' | 'error') {
    statusMessage = msg;
    statusType = type;
    setTimeout(() => {
      statusMessage = '';
    }, STATUS_DISMISS_MS);
  }

  // Data loading
  async function loadRules() {
    loadingRules = true;
    try {
      rules = await fetchAlertRules();
    } catch (err) {
      logger.error('Failed to load alert rules', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.loadFailed'), 'error');
    } finally {
      loadingRules = false;
    }
  }

  async function loadSchema() {
    try {
      schema = await fetchAlertSchema();
    } catch (err) {
      logger.error('Failed to load alert schema', err, { component: 'AlertRulesSettingsPage' });
    }
  }

  async function loadHistory() {
    loadingHistory = true;
    try {
      const resp = await fetchAlertHistory({ limit: HISTORY_FETCH_LIMIT });
      history = resp.history;
      historyTotal = resp.total;
    } catch (err) {
      logger.error('Failed to load alert history', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.historyFailed'), 'error');
    } finally {
      loadingHistory = false;
    }
  }

  // Actions
  async function handleToggle(rule: AlertRule) {
    togglingId = rule.id;
    try {
      await toggleAlertRule(rule.id, !rule.enabled);
      rule.enabled = !rule.enabled;
      showStatus(
        rule.enabled ? t('settings.alerts.status.enabled') : t('settings.alerts.status.disabled'),
        'success'
      );
    } catch (err) {
      logger.error('Failed to toggle rule', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.toggleFailed'), 'error');
    } finally {
      togglingId = null;
    }
  }

  async function handleDelete(rule: AlertRule) {
    if (!window.confirm(t('settings.alerts.confirmDelete', { name: rule.name }))) return;
    deletingId = rule.id;
    try {
      await deleteAlertRule(rule.id);
      rules = rules.filter(r => r.id !== rule.id);
      showStatus(t('settings.alerts.status.deleted'), 'success');
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        rules = rules.filter(r => r.id !== rule.id);
      } else {
        logger.error('Failed to delete rule', err, { component: 'AlertRulesSettingsPage' });
        showStatus(t('settings.alerts.errors.deleteFailed'), 'error');
      }
    } finally {
      deletingId = null;
    }
  }

  async function handleTest(rule: AlertRule) {
    testingId = rule.id;
    try {
      await testAlertRule(rule.id);
      showStatus(t('settings.alerts.status.testFired'), 'success');
    } catch (err) {
      logger.error('Failed to test rule', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.testFailed'), 'error');
    } finally {
      testingId = null;
    }
  }

  async function handleResetDefaults() {
    resetting = true;
    try {
      await resetAlertDefaults();
      await loadRules();
      showStatus(t('settings.alerts.status.defaultsReset'), 'success');
    } catch (err) {
      logger.error('Failed to reset defaults', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.resetFailed'), 'error');
    } finally {
      resetting = false;
    }
  }

  async function handleClearHistory() {
    clearingHistory = true;
    try {
      await clearAlertHistory();
      history = [];
      historyTotal = 0;
      showStatus(t('settings.alerts.status.historyCleared'), 'success');
    } catch (err) {
      logger.error('Failed to clear history', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.clearHistoryFailed'), 'error');
    } finally {
      clearingHistory = false;
    }
  }

  function openEditor(rule: AlertRule | null = null) {
    if (!schema) {
      showStatus(t('settings.alerts.errors.schemaLoadFailed'), 'error');
      return;
    }
    editingRule = rule;
    editorOpen = true;
  }

  function closeEditor() {
    editorOpen = false;
    editingRule = null;
  }

  async function handleEditorSave(data: Partial<AlertRule>) {
    try {
      if (data.id) {
        await updateAlertRule(data.id, data);
        showStatus(t('settings.alerts.status.updated'), 'success');
      } else {
        await createAlertRule(data);
        showStatus(t('settings.alerts.status.created'), 'success');
      }
      closeEditor();
      await loadRules();
    } catch (err) {
      logger.error('Failed to save alert rule', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.saveFailed'), 'error');
    }
  }

  async function handleExport() {
    exporting = true;
    try {
      const data = await exportAlertRules();
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = 'alert-rules.json';
      a.click();
      URL.revokeObjectURL(url);
      showStatus(t('settings.alerts.status.exported'), 'success');
    } catch (err) {
      logger.error('Failed to export rules', err, { component: 'AlertRulesSettingsPage' });
      showStatus(t('settings.alerts.errors.exportFailed'), 'error');
    } finally {
      exporting = false;
    }
  }

  function handleImport() {
    const input = document.createElement('input');
    input.type = 'file';
    input.accept = '.json';
    input.onchange = async () => {
      const file = input.files?.[0];
      if (!file) return;
      importing = true;
      try {
        const text = await file.text();
        const data = JSON.parse(text);
        const result = await importAlertRules(data.rules ?? [], data.version ?? 1);
        await loadRules();
        showStatus(
          t('settings.alerts.status.imported', {
            imported: String(result.imported),
            total: String(result.total),
          }),
          'success'
        );
      } catch (err) {
        logger.error('Failed to import rules', err, { component: 'AlertRulesSettingsPage' });
        showStatus(t('settings.alerts.errors.importFailed'), 'error');
      } finally {
        importing = false;
      }
    };
    input.click();
  }

  onMount(() => {
    loadSchema();
    loadRules();
    loadHistory();
  });
</script>

{#snippet statusBanner()}
  {#if statusMessage}
    <div
      class="mb-4 flex items-center gap-2 rounded-lg p-3 text-sm {statusType === 'success'
        ? 'bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400'
        : statusType === 'error'
          ? 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400'
          : 'bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400'}"
      role={statusType === 'error' ? 'alert' : 'status'}
      aria-live={statusType === 'error' ? 'assertive' : 'polite'}
    >
      {#if statusType === 'success'}
        <CircleCheck class="h-4 w-4 shrink-0" />
      {:else if statusType === 'error'}
        <XCircle class="h-4 w-4 shrink-0" />
      {:else}
        <Info class="h-4 w-4 shrink-0" />
      {/if}
      <span>{statusMessage}</span>
    </div>
  {/if}
{/snippet}

{#snippet ruleCard(rule: AlertRule)}
  <div
    class="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800"
    class:opacity-60={!rule.enabled}
  >
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <h4 class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">
            {rule.name}
          </h4>
          {#if rule.built_in}
            <span
              class="inline-flex items-center gap-1 rounded-full bg-blue-100 px-2 py-0.5 text-xs font-medium text-blue-700 dark:bg-blue-900/30 dark:text-blue-400"
            >
              <Shield class="h-3 w-3" />
              {t('settings.alerts.builtIn')}
            </span>
          {/if}
          <span
            class="inline-flex rounded-full px-2 py-0.5 text-xs font-medium {rule.enabled
              ? 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400'
              : 'bg-gray-100 text-gray-500 dark:bg-gray-700 dark:text-gray-400'}"
          >
            {rule.enabled ? t('settings.alerts.enabled') : t('settings.alerts.disabled')}
          </span>
        </div>
        {#if rule.description}
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{rule.description}</p>
        {/if}
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500 dark:text-gray-400">
          <span>
            <span class="font-medium">{t('settings.alerts.trigger')}:</span>
            {objectTypeLabel(rule.object_type)} &rarr; {triggerLabel(rule)}
          </span>
          <span>
            <span class="font-medium">{t('settings.alerts.conditions')}:</span>
            {conditionsSummary(rule)}
          </span>
          <span>
            <span class="font-medium">{t('settings.alerts.actions')}:</span>
            {actionsSummary(rule)}
          </span>
          <span>
            <span class="font-medium">{t('settings.alerts.cooldown')}:</span>
            {formatCooldown(rule.cooldown_sec)}
          </span>
        </div>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <button
          class="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700 dark:hover:text-gray-300"
          aria-label={t('settings.alerts.actionLabels.edit')}
          onclick={() => openEditor(rule)}
        >
          <Pencil class="h-4 w-4" />
        </button>
        <button
          class="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700 dark:hover:text-gray-300"
          aria-label={rule.enabled
            ? t('settings.alerts.actionLabels.disable')
            : t('settings.alerts.actionLabels.enable')}
          disabled={togglingId === rule.id}
          onclick={() => handleToggle(rule)}
        >
          {#if rule.enabled}
            <ToggleRight class="h-4 w-4 text-green-500" />
          {:else}
            <ToggleLeft class="h-4 w-4" />
          {/if}
        </button>
        <button
          class="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-700 dark:hover:text-gray-300"
          aria-label={t('settings.alerts.actionLabels.test')}
          disabled={testingId === rule.id}
          onclick={() => handleTest(rule)}
        >
          <Play class="h-4 w-4" />
        </button>
        <button
          class="rounded p-1.5 text-gray-400 hover:bg-gray-100 hover:text-red-500 dark:hover:bg-gray-700 dark:hover:text-red-400"
          aria-label={t('settings.alerts.actionLabels.delete')}
          disabled={deletingId === rule.id}
          onclick={() => handleDelete(rule)}
        >
          <Trash2 class="h-4 w-4" />
        </button>
      </div>
    </div>
  </div>
{/snippet}

{#snippet rulesTabContent()}
  {@render statusBanner()}
  <SettingsSection
    title={t('settings.alerts.sections.rules.title')}
    description={t('settings.alerts.sections.rules.description')}
    defaultOpen={true}
  >
    <!-- Filters -->
    <div class="mb-4 flex flex-wrap items-center gap-3">
      <div class="w-48">
        <SelectDropdown
          options={objectTypeOptions}
          bind:value={filterObjectType}
          label={t('settings.alerts.filters.objectType')}
        />
      </div>
      <div class="w-40">
        <SelectDropdown
          options={enabledOptions}
          bind:value={filterEnabled}
          label={t('settings.alerts.filters.status')}
        />
      </div>
      <div class="ml-auto flex items-center gap-2">
        <SettingsButton
          variant="secondary"
          onclick={handleExport}
          loading={exporting}
          loadingText={t('settings.alerts.exporting')}
        >
          <Download class="mr-1.5 h-4 w-4" />
          {t('settings.alerts.export')}
        </SettingsButton>
        <SettingsButton
          variant="secondary"
          onclick={handleImport}
          loading={importing}
          loadingText={t('settings.alerts.importing')}
        >
          <Upload class="mr-1.5 h-4 w-4" />
          {t('settings.alerts.import')}
        </SettingsButton>
        <SettingsButton
          variant="secondary"
          onclick={handleResetDefaults}
          loading={resetting}
          loadingText={t('settings.alerts.resetting')}
        >
          <RotateCcw class="mr-1.5 h-4 w-4" />
          {t('settings.alerts.resetDefaults')}
        </SettingsButton>
        <SettingsButton variant="primary" onclick={() => openEditor()}>
          <Plus class="mr-1.5 h-4 w-4" />
          {t('settings.alerts.newRule')}
        </SettingsButton>
      </div>
    </div>

    <!-- Rules list -->
    {#if loadingRules}
      <div class="flex justify-center py-8" role="status" aria-live="polite">
        <div
          class="h-6 w-6 animate-spin rounded-full border-2 border-blue-500 border-t-transparent"
        ></div>
        <span class="sr-only">{t('common.loading')}</span>
      </div>
    {:else if filteredRules.length === 0}
      <div class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {rules.length === 0 ? t('settings.alerts.noRules') : t('settings.alerts.noMatchingRules')}
      </div>
    {:else}
      <div class="space-y-3">
        {#each filteredRules as rule (rule.id)}
          {@render ruleCard(rule)}
        {/each}
      </div>
    {/if}
  </SettingsSection>
{/snippet}

{#snippet historyTabContent()}
  {@render statusBanner()}
  <SettingsSection
    title={t('settings.alerts.sections.history.title')}
    description={t('settings.alerts.sections.history.description')}
    defaultOpen={true}
  >
    <div class="mb-4 flex items-center justify-between">
      <span class="text-sm text-gray-500 dark:text-gray-400">
        {t('settings.alerts.historyCount', { total: String(historyTotal) })}
      </span>
      <SettingsButton
        variant="secondary"
        onclick={handleClearHistory}
        loading={clearingHistory}
        loadingText={t('settings.alerts.clearing')}
        disabled={history.length === 0}
      >
        <Trash2 class="mr-1.5 h-4 w-4" />
        {t('settings.alerts.clearHistory')}
      </SettingsButton>
    </div>

    {#if loadingHistory}
      <div class="flex justify-center py-8" role="status" aria-live="polite">
        <div
          class="h-6 w-6 animate-spin rounded-full border-2 border-blue-500 border-t-transparent"
        ></div>
        <span class="sr-only">{t('common.loading')}</span>
      </div>
    {:else if history.length === 0}
      <div class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
        {t('settings.alerts.noHistory')}
      </div>
    {:else}
      <div class="divide-y divide-gray-200 dark:divide-gray-700">
        {#each history as entry (entry.id)}
          <div class="py-3">
            <div class="flex items-center justify-between">
              <span class="text-sm font-medium text-gray-900 dark:text-gray-100">
                {entry.rule?.name ?? `Rule #${entry.rule_id}`}
              </span>
              <span class="text-xs text-gray-500 dark:text-gray-400">
                {formatLocalDateTime(new Date(entry.fired_at), false)}
              </span>
            </div>
            {#if entry.actions}
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {t('settings.alerts.actionsExecuted')}: {entry.actions}
              </p>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </SettingsSection>
{/snippet}

<main class="settings-page-content" aria-label="Alert rules settings">
  <SettingsTabs {tabs} bind:activeTab showActions={false} />
</main>

{#if schema}
  <AlertRuleEditor
    isOpen={editorOpen}
    rule={editingRule}
    {schema}
    onSave={handleEditorSave}
    onClose={closeEditor}
  />
{/if}
