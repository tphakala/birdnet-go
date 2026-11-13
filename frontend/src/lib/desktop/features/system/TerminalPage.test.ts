import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render } from '@testing-library/svelte';
import { tick } from 'svelte';
import { get } from 'svelte/store';

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    textarea = document.createElement('textarea');
    cols = 80;
    rows = 24;
    options = {};
    loadAddon() {}
    open() {}
    focus() {}
    dispose() {}
    onData() {
      return { dispose() {} };
    }
    onResize() {
      return { dispose() {} };
    }
    attachCustomKeyEventHandler() {}
    getSelection() {
      return '';
    }
  },
}));
vi.mock('@xterm/addon-fit', () => ({
  FitAddon: class {
    fit() {}
  },
}));
vi.mock('@xterm/xterm/css/xterm.css', () => ({}));
vi.mock('@xterm/xterm/css/xterm.css?raw', () => ({ default: '' }));

import { settingsStore } from '$lib/stores/settings';
import TerminalPage from './TerminalPage.svelte';

// The test setup returns translation keys as text
const TOOLBAR_BUTTON_LABELS = [
  'terminal.colorTheme',
  'terminal.copySelection',
  'terminal.detach',
  'terminal.expand',
  'terminal.fullscreen',
];

function enableTerminal() {
  const state = get(settingsStore);
  settingsStore.set({
    ...state,
    originalData: {
      ...state.originalData,
      webServer: { ...state.originalData.webServer, enableTerminal: true },
    },
  });
}

describe('TerminalPage toolbar', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'WebSocket',
      class {
        close() {}
      }
    );
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe() {}
        disconnect() {}
      }
    );
    enableTerminal();
  });

  it('keeps every toolbar button in the Tab order', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      expect(getByRole('button', { name: label }).getAttribute('tabindex')).toBeNull();
    }
  });

  it('dims toolbar icons through colour, so the focus ring stays at full strength', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      const button = getByRole('button', { name: label });
      expect(button.style.opacity).toBe('');
      expect(button.className).not.toMatch(/(^|\s)(hover:)?opacity-/);
    }
  });

  it('keeps a real outline on keyboard focus so forced colors still shows one', async () => {
    const { getByRole } = render(TerminalPage);
    await tick();
    for (const label of TOOLBAR_BUTTON_LABELS) {
      const classes = getByRole('button', { name: label }).className;
      expect(classes).toContain('focus-visible:outline-2');
      expect(classes).not.toContain('focus-visible:outline-none');
    }
  });
});
