/**
 * SettingsGroup: a titled group of an immediately saved tab. It shows the status
 * of its field beside the title and, while that field shows an error, describes
 * the group by the error message.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';

const store = vi.hoisted(() => ({
  stageSectionEdit: vi.fn(),
  saveStagedSection: vi.fn(),
  onStagedEditsDropped: vi.fn(() => () => undefined),
}));

vi.mock('$lib/stores/settings', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/stores/settings')>();
  return { ...actual, settingsActions: { ...actual.settingsActions, ...store } };
});

import { commitSetting, resetAutoSaveForTest } from '$lib/stores/settingsAutoSave.svelte';
import SettingsGroup from './SettingsGroup.svelte';

const FIELD = 'audio:sources:r:gain';
const STATUS_ID = 'gain-status';

function renderGroup() {
  return render(SettingsGroup, {
    props: {
      title: 'Level',
      field: FIELD,
      statusId: STATUS_ID,
      headingId: 'gain-heading',
      children: createRawSnippet(() => ({ render: () => '<p>controls</p>' })),
    },
  });
}

describe('SettingsGroup', () => {
  beforeEach(() => {
    resetAutoSaveForTest();
    store.stageSectionEdit.mockReset();
    store.saveStagedSection.mockReset();
  });

  afterEach(() => {
    cleanup();
    resetAutoSaveForTest();
  });

  it('is labelled by its title and has no description while the field is fine', () => {
    renderGroup();

    const group = screen.getByRole('region', { name: 'Level' });
    expect(group).not.toHaveAttribute('aria-describedby');
  });

  it('is described by the error message while the field shows an error', async () => {
    renderGroup();
    // Staging is refused, so the field ends in error at once.
    store.stageSectionEdit.mockImplementation(() => {
      throw new Error('settings are not loaded');
    });

    commitSetting('audio', { sources: [] }, { field: FIELD });

    await vi.waitFor(() =>
      expect(screen.getByRole('region', { name: 'Level' })).toHaveAttribute(
        'aria-describedby',
        STATUS_ID
      )
    );
    expect(document.getElementById(STATUS_ID)).toHaveTextContent('settings.autoSave.failed');
  });
});
