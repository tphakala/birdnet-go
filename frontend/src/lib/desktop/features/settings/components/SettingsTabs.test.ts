import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
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
