import { afterEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import DashboardEditMode from './DashboardEditMode.svelte';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';
import type { DashboardLayout } from '$lib/stores/settings';

// Date changes on the dashboard are query-only, so the guard only sees path changes.
const FROM: NavigationTarget = { path: ['', 'ui', 'dashboard'].join('/'), search: '' };
const TO: NavigationTarget = { path: ['', 'ui', 'detections'].join('/'), search: '' };

const layout: DashboardLayout = {
  elements: [
    { id: 'daily-summary-0', type: 'daily-summary', enabled: true },
    { id: 'detections-grid-0', type: 'detections-grid', enabled: true },
  ],
};

const renderElement = createRawSnippet(() => ({ render: () => '<div>element</div>' }));

describe('DashboardEditMode unsaved-changes guard', () => {
  afterEach(() => {
    cleanup();
    unsavedChanges.resetForTesting();
  });

  function renderEditing() {
    const onEditModeChange = vi.fn();
    render(DashboardEditMode, {
      props: { layout, editMode: true, onLayoutChange: vi.fn(), onEditModeChange, renderElement },
    });
    return { onEditModeChange };
  }

  it('does not block an untouched edit session', () => {
    renderEditing();

    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);
  });

  it('blocks once an element is hidden', async () => {
    renderEditing();

    const [hide] = screen.getAllByRole('button', { name: 'dashboard.editMode.hideElement' });
    await fireEvent.click(hide);

    expect(unsavedChanges.blocks(TO, FROM)).toBe(true);
  });

  it('does not block outside edit mode', () => {
    render(DashboardEditMode, {
      props: {
        layout,
        editMode: false,
        onLayoutChange: vi.fn(),
        onEditModeChange: vi.fn(),
        renderElement,
      },
    });

    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);
  });

  it('stops blocking after Cancel', async () => {
    renderEditing();
    const [hide] = screen.getAllByRole('button', { name: 'dashboard.editMode.hideElement' });
    await fireEvent.click(hide);

    await fireEvent.click(screen.getByRole('button', { name: 'dashboard.editMode.cancel' }));

    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);
  });
});
