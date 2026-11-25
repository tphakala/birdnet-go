import { describe, it, expect, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import AlertRuleEditor from './AlertRuleEditor.svelte';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';
import type { AlertRule, AlertSchema } from '$lib/api/alerts';

const PAGE = ['', 'ui', 'settings', 'notifications'].join('/');
const HERE: NavigationTarget = { path: PAGE, search: '?tab=rules' };
const OTHER_TAB: NavigationTarget = { path: PAGE, search: '?tab=channels' };
const OTHER_PAGE: NavigationTarget = { path: ['', 'ui', 'dashboard'].join('/'), search: '' };

const schema: AlertSchema = {
  objectTypes: [
    {
      name: 'detection',
      label: 'Detection',
      events: [{ name: 'new_species', label: 'New species', properties: [] }],
    },
  ],
  operators: [],
};

const rule: AlertRule = {
  id: 7,
  name: 'Rare bird',
  description: 'Tell me about rarities',
  enabled: true,
  built_in: false,
  object_type: 'detection',
  trigger_type: 'event',
  event_name: 'new_species',
  metric_name: '',
  cooldown_sec: 300,
  created_at: '',
  updated_at: '',
  conditions: [
    {
      id: 1,
      rule_id: 7,
      property: 'confidence',
      operator: '>',
      value: '0.8',
      duration_sec: 0,
      sort_order: 0,
    },
  ],
  actions: [
    { id: 1, rule_id: 7, target: 'bell', template_title: '', template_message: '', sort_order: 0 },
  ],
};

describe('AlertRuleEditor unsaved-changes guard', () => {
  afterEach(() => {
    unsavedChanges.resetForTesting();
  });

  function renderEditor() {
    return render(AlertRuleEditor, {
      props: { rule, schema, onSave: () => {}, onClose: () => {} },
    });
  }

  it('does not block an untouched rule with conditions', async () => {
    renderEditor();
    await screen.findByLabelText('settings.alerts.editor.name');

    expect(unsavedChanges.blocks(OTHER_TAB, HERE)).toBe(false);
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(false);
  });

  it('blocks a tab switch and a path change after the name changes', async () => {
    const user = userEvent.setup();
    renderEditor();

    await user.type(await screen.findByLabelText('settings.alerts.editor.name'), ' 2');

    expect(unsavedChanges.blocks(OTHER_TAB, HERE)).toBe(true);
    expect(unsavedChanges.blocks(OTHER_PAGE, HERE)).toBe(true);
  });
});
