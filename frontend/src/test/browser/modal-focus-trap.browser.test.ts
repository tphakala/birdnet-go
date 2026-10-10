/**
 * Browser Mode Test: Modal keeps Tab inside the dialog
 *
 * The jsdom tests rely on user-event's own model of Tab order, so only a real
 * browser shows whether the trap lets a native Tab leave the dialog. The
 * wrapper ends with a disabled, a visibility:hidden and a display:none button
 * and has a leading named radio group, the shapes that escaped the old trap.
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect } from 'vitest';
import { render } from 'vitest-browser-svelte';
import { userEvent } from 'vitest/browser';

import ModalDisabledLast from './wrappers/ModalDisabledLast.svelte';

const TAB_PRESSES = 8;

function blurActiveElement() {
  if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
}

function activeIsInsideDialog(): boolean {
  const dialog = document.querySelector('[role="dialog"]');
  return !!dialog && dialog.contains(document.activeElement);
}

async function waitForInitialFocus() {
  await expect.poll(activeIsInsideDialog).toBe(true);
}

describe('Modal focus trap in a real browser', () => {
  it('Tab never leaves the dialog when its last buttons are disabled or hidden', async () => {
    await render(ModalDisabledLast, {});
    await waitForInitialFocus();

    for (let i = 0; i < TAB_PRESSES; i++) {
      await userEvent.tab();
      expect(activeIsInsideDialog()).toBe(true);
    }
  });

  it('Shift+Tab never leaves the dialog from the checked radio of a leading radio group', async () => {
    await render(ModalDisabledLast, {});
    await waitForInitialFocus();
    document.querySelector<HTMLElement>('input[value="b"]')?.focus();

    for (let i = 0; i < TAB_PRESSES; i++) {
      await userEvent.tab({ shift: true });
      expect(activeIsInsideDialog()).toBe(true);
    }
  });

  it('Tab returns into the dialog when focus is on body', async () => {
    await render(ModalDisabledLast, {});
    await waitForInitialFocus();
    blurActiveElement();

    await userEvent.tab();

    expect(activeIsInsideDialog()).toBe(true);
  });
});
