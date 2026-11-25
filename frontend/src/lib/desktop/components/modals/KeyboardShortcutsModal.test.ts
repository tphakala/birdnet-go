import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { tick } from 'svelte';
import KeyboardShortcutsModal from './KeyboardShortcutsModal.svelte';
import Modal from '$lib/desktop/components/ui/Modal.svelte';
import {
  SHORTCUTS_ENABLED_STORAGE_KEY,
  shortcuts,
  type ShortcutDefinition,
} from '$lib/stores/shortcuts.svelte';
import { KEYS } from '$lib/utils/keyboard';
import { expectNoA11yViolations } from '$lib/utils/axe-utils';

const PAGE_SCOPE_ID = 'test-page';
const PAGE_LABEL = 'Test page';
const GLOBAL_LABEL = 'shortcuts.scopes.global';
const DIALOG_NAME = 'shortcuts.modal.title';
const ENABLE_LABEL = 'shortcuts.modal.enable';

function definition(
  id: string,
  group: ShortcutDefinition['group'],
  key: string,
  overrides: Partial<ShortcutDefinition> = {}
): ShortcutDefinition {
  return {
    id,
    group,
    binding: { key },
    description: () => `describe ${id}`,
    run: vi.fn(),
    ...overrides,
  };
}

/** The page's definitions, registered out of group order on purpose. */
const pageDefinitions: ShortcutDefinition[] = [
  definition('play', 'audio', 'p', { secondaryBinding: { key: KEYS.space } }),
  definition('correct', 'review', 'c', { visible: () => false }),
  definition('older', 'navigation', KEYS.arrowLeft),
];

function pressQuestionMark() {
  fireEvent.keyDown(document.activeElement ?? document.body, {
    key: KEYS.questionMark,
    shiftKey: true,
  });
}

const dialog = () => screen.queryByRole('dialog', { name: DIALOG_NAME });

describe('KeyboardShortcutsModal', () => {
  let unregisterPage: (() => void) | undefined;

  beforeEach(() => {
    window.addEventListener('keydown', shortcuts.handleKeydown);
  });

  afterEach(() => {
    unregisterPage?.();
    unregisterPage = undefined;
    cleanup();
    window.removeEventListener('keydown', shortcuts.handleKeydown);
    shortcuts.setEnabled(true);
    shortcuts.closeHelp();
    localStorage.clear();
  });

  it('lists the page section before All pages, with groups in a fixed order', async () => {
    unregisterPage = shortcuts.register({
      id: PAGE_SCOPE_ID,
      label: () => PAGE_LABEL,
      definitions: pageDefinitions,
    });
    render(KeyboardShortcutsModal);
    shortcuts.openHelp();
    await tick();

    const sheet = within(screen.getByRole('dialog', { name: DIALOG_NAME }));
    const headings = sheet.getAllByRole('heading').map(heading => heading.textContent.trim());
    expect(headings).toEqual([
      DIALOG_NAME,
      PAGE_LABEL,
      'shortcuts.groups.navigation',
      'shortcuts.groups.audio',
      GLOBAL_LABEL,
      'shortcuts.groups.general',
    ]);
    // Hidden from the sheet, not from the keyboard
    expect(sheet.queryByText('describe correct')).toBeNull();
    // The second key, after "or", with its spoken form for screen readers
    const playRow = sheet.getByText('describe play').closest('div');
    expect(playRow).toHaveTextContent('shortcuts.modal.or');
    expect(playRow).toHaveTextContent('shortcuts.keys.space');
  });

  it('turns shortcuts off from its switch, which stops ? and persists, and back on', async () => {
    render(KeyboardShortcutsModal);
    shortcuts.openHelp();
    await tick();

    await fireEvent.click(screen.getByRole('switch', { name: new RegExp(ENABLE_LABEL) }));
    expect(shortcuts.enabled).toBe(false);
    expect(localStorage.getItem(SHORTCUTS_ENABLED_STORAGE_KEY)).toBe('false');
    expect(screen.getByText('shortcuts.modal.offNote')).toBeInTheDocument();

    shortcuts.closeHelp();
    await tick();
    pressQuestionMark();
    await tick();
    expect(shortcuts.helpOpen).toBe(false);

    shortcuts.openHelp();
    await tick();
    await fireEvent.click(screen.getByRole('switch', { name: new RegExp(ENABLE_LABEL) }));
    shortcuts.closeHelp();
    await tick();
    pressQuestionMark();
    await tick();
    expect(shortcuts.helpOpen).toBe(true);
  });

  it('opens and closes with ?, and leaves a dialog opened over it alone', async () => {
    render(KeyboardShortcutsModal);

    pressQuestionMark();
    await tick();
    expect(dialog()).toHaveClass('visible');

    pressQuestionMark();
    await tick();
    expect(shortcuts.helpOpen).toBe(false);

    pressQuestionMark();
    await tick();
    render(Modal, { props: { isOpen: true, title: 'Other dialog' } });
    await tick();
    pressQuestionMark();
    await tick();
    expect(shortcuts.helpOpen).toBe(true);
    expect(screen.getByRole('dialog', { name: 'Other dialog' })).toHaveClass('visible');
  });

  it('does not stay open after it unmounts', async () => {
    const { unmount } = render(KeyboardShortcutsModal);
    shortcuts.openHelp();
    await tick();

    unmount();

    expect(shortcuts.helpOpen).toBe(false);
  });
});

describe('KeyboardShortcutsModal Accessibility', () => {
  afterEach(() => {
    cleanup();
    shortcuts.closeHelp();
  });

  it('has no violations while open', async () => {
    const { container } = render(KeyboardShortcutsModal);
    shortcuts.openHelp();
    await tick();

    expect(dialog()).toHaveClass('visible');
    await expectNoA11yViolations(container);
  });
});
