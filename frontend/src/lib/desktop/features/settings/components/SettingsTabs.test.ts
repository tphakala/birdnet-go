import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { settingsActions } from '$lib/stores/settings';
import { commitSetting, resetAutoSaveForTest } from '$lib/stores/settingsAutoSave.svelte';
import SettingsTabs, { type TabDefinition } from './SettingsTabs.svelte';

const HAS_CHANGES_TEXT = 'settings.tabs.hasChanges';

function tab(id: string, hasChanges: boolean): TabDefinition {
  return {
    id,
    label: `Label ${id}`,
    hasChanges,
    content: createRawSnippet(() => ({ render: () => `<p>content ${id}</p>` })),
  };
}

function renderTabs(tabs: TabDefinition[]) {
  return render(SettingsTabs, { props: { tabs, activeTab: tabs[0].id, showActions: false } });
}

describe('SettingsTabs change indicator', () => {
  it('renders a sized dot on a tab with unsaved changes and describes the tab by it', () => {
    renderTabs([tab('one', true), tab('two', false)]);

    const changed = screen.getByRole('tab', { name: 'Label one' });
    const dot = changed.querySelector('[data-testid="tab-change-dot"]');
    expect(dot).not.toBeNull();
    expect(dot).toHaveClass('size-2.5', 'rounded-full');
    expect(dot).not.toHaveClass('badge');
    expect(dot).toHaveAttribute('aria-hidden', 'true');
    expect(changed).toHaveAccessibleDescription(HAS_CHANGES_TEXT);
  });

  it('renders no dot and no description on a tab without changes', () => {
    renderTabs([tab('one', true), tab('two', false)]);

    const clean = screen.getByRole('tab', { name: 'Label two' });
    expect(clean.querySelector('[data-testid="tab-change-dot"]')).toBeNull();
    expect(clean).not.toHaveAttribute('aria-describedby');
    expect(clean).toHaveAccessibleDescription('');
  });
});

describe('SettingsTabs tablist label', () => {
  it('names the tablist settings.tabs.navigation by default', () => {
    renderTabs([tab('one', false)]);

    expect(screen.getByRole('tablist', { name: 'settings.tabs.navigation' })).toBeInTheDocument();
  });

  it('names the tablist settings.tabs.navigation when ariaLabel is empty', () => {
    render(SettingsTabs, {
      props: { tabs: [tab('one', false)], activeTab: 'one', showActions: false, ariaLabel: '' },
    });

    expect(screen.getByRole('tablist', { name: 'settings.tabs.navigation' })).toBeInTheDocument();
  });

  it('names the tablist settings.tabs.navigation when ariaLabel is only whitespace', () => {
    render(SettingsTabs, {
      props: { tabs: [tab('one', false)], activeTab: 'one', showActions: false, ariaLabel: '  ' },
    });

    expect(screen.getByRole('tablist', { name: 'settings.tabs.navigation' })).toBeInTheDocument();
  });

  it('names the tablist by ariaLabel when given', () => {
    render(SettingsTabs, {
      props: { tabs: [tab('one', false)], activeTab: 'one', showActions: false, ariaLabel: 'Own' },
    });

    expect(screen.getByRole('tablist', { name: 'Own' })).toBeInTheDocument();
    expect(screen.queryByRole('tablist', { name: 'settings.tabs.navigation' })).toBeNull();
  });
});

describe('SettingsTabs tab styling', () => {
  it('leaves the label colour to .tab and marks only the selected tab', () => {
    renderTabs([tab('one', false), tab('two', false), tab('three', false)]);

    for (const element of screen.getAllByRole('tab')) {
      expect(element.className).not.toContain('text-[');
      expect(element.className).not.toContain('opacity-');
    }
    expect(screen.getByRole('tab', { name: 'Label one' })).toHaveClass('tab-active');
    expect(screen.getByRole('tab', { name: 'Label two' })).not.toHaveClass('tab-active');
    expect(screen.getByRole('tab', { name: 'Label three' })).not.toHaveClass('tab-active');
  });
});

describe('SettingsTabs save mode', () => {
  const SAVE_NAME = 'settings.actions.saveAriaLabel';
  const NOTE_TEXT = 'settings.autoSave.tabNote';

  function modeTab(id: string, saveMode: 'manual' | 'immediate'): TabDefinition {
    return { ...tab(id, false), saveMode };
  }

  function renderWithBar(tabs: TabDefinition[], activeTab: string, showActions = true) {
    window.history.replaceState(null, '', `/ui/settings/audio?tab=${activeTab}`);
    return render(SettingsTabs, { props: { tabs, activeTab, showActions } });
  }

  beforeEach(() => {
    vi.spyOn(console, 'warn').mockImplementation(() => undefined);
  });

  afterEach(() => {
    resetAutoSaveForTest();
    vi.restoreAllMocks();
    window.history.replaceState(null, '', '/');
  });

  it('shows the note and no Save button on an immediate tab', () => {
    renderWithBar([modeTab('live', 'immediate'), modeTab('bar', 'manual')], 'live');

    expect(screen.getByText(NOTE_TEXT)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: SAVE_NAME })).toBeNull();
  });

  it('brings the save bar back on a manual tab and leaves out the note', () => {
    renderWithBar([modeTab('live', 'immediate'), modeTab('bar', 'manual')], 'bar');

    expect(screen.getByRole('button', { name: SAVE_NAME })).toBeInTheDocument();
    expect(screen.queryByText(NOTE_TEXT)).toBeNull();
  });

  it('summarises fields that were not saved in a status line on an immediate tab', async () => {
    renderWithBar([modeTab('live', 'immediate')], 'live');

    // Settings have not loaded, so staging is refused and the field ends in error.
    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });

    expect(await screen.findByRole('status')).toHaveTextContent(
      'settings.autoSave.failuresSummary'
    );
  });

  it('says Saving in the status line while a field waits for its save', async () => {
    vi.spyOn(settingsActions, 'stageSectionEdit').mockImplementation(() => undefined);
    vi.spyOn(settingsActions, 'saveStagedSection').mockReturnValue(new Promise(() => undefined));
    renderWithBar([modeTab('live', 'immediate')], 'live');

    commitSetting('audio', { sources: [] }, { field: 'audio:a:models' });

    expect(await screen.findByRole('status')).toHaveTextContent('settings.actions.saving');
  });

  it('counts only the failures and saves of its own fields, given a scope', async () => {
    const scoped: TabDefinition = {
      ...modeTab('live', 'immediate'),
      autoSaveScope: 'audio:sources:',
    };
    renderWithBar([scoped], 'live');

    // Settings have not loaded, so staging is refused and the field ends in error.
    commitSetting('rtsp', { streams: [] }, { field: 'rtsp:s:name' });
    await Promise.resolve();
    expect(screen.getByRole('status')).toHaveTextContent('');

    commitSetting('audio', { sources: [] }, { field: 'audio:sources:a:models' });
    expect(await screen.findByRole('status')).toHaveTextContent(
      'settings.autoSave.failuresSummary'
    );
  });

  it('says Saving only for fields of its own scope', async () => {
    vi.spyOn(settingsActions, 'stageSectionEdit').mockImplementation(() => undefined);
    vi.spyOn(settingsActions, 'saveStagedSection').mockReturnValue(new Promise(() => undefined));
    renderWithBar([{ ...modeTab('live', 'immediate'), autoSaveScope: 'audio:sources:' }], 'live');

    commitSetting('rtsp', { streams: [] }, { field: 'rtsp:s:name' });
    await Promise.resolve();
    expect(screen.getByRole('status')).toHaveTextContent('');

    commitSetting('audio', { sources: [] }, { field: 'audio:sources:a:models' });
    expect(await screen.findByRole('status')).toHaveTextContent('settings.actions.saving');
  });

  it('names the other tabs with unsaved changes and opens one on request', async () => {
    renderWithBar(
      [modeTab('live', 'immediate'), { ...modeTab('rec', 'manual'), hasChanges: true }],
      'live'
    );

    expect(screen.getByText('settings.autoSave.otherTabsUnsaved')).toBeInTheDocument();
    // Saving them stays on their own tab: no second save action here.
    expect(screen.queryByRole('button', { name: SAVE_NAME })).toBeNull();

    await fireEvent.click(screen.getByRole('button', { name: 'Label rec' }));
    expect(screen.getByRole('tab', { name: 'Label rec' })).toHaveAttribute('aria-selected', 'true');
  });

  it('says nothing about other tabs when none has unsaved changes', () => {
    renderWithBar([modeTab('live', 'immediate'), modeTab('rec', 'manual')], 'live');

    expect(screen.queryByText('settings.autoSave.otherTabsUnsaved')).toBeNull();
  });

  it('treats a tab without a save mode as manual', () => {
    renderWithBar([tab('plain', false)], 'plain');

    expect(screen.getByRole('button', { name: SAVE_NAME })).toBeInTheDocument();
  });

  it('shows neither the note nor the bar when actions are hidden', () => {
    renderWithBar([modeTab('live', 'immediate')], 'live', false);

    expect(screen.queryByText(NOTE_TEXT)).toBeNull();
    expect(screen.queryByRole('button', { name: SAVE_NAME })).toBeNull();
  });
});
