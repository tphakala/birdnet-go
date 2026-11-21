import { afterEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { unsavedChanges } from './unsavedChanges.svelte';
import Host from './unsavedChanges.pendingEdit.test.svelte';

const FROM = { path: ['', 'ui', 'a'].join('/'), search: '' };
const TO = { path: ['', 'ui', 'b'].join('/'), search: '' };

// TextInput commits on change, so text still being typed is not in the draft yet. The guard
// commits it (blur) before it decides.
describe('unsaved changes with an edit still being typed', () => {
  afterEach(() => {
    unsavedChanges.resetForTesting();
  });

  async function typeWithoutLeavingTheField() {
    render(Host);
    // user-event fires the change event when the field loses focus, as a browser does.
    await userEvent.type(screen.getByLabelText('Name'), ' edited');
  }

  it('holds a move made while the field is still focused', async () => {
    await typeWithoutLeavingTheField();

    const decision = unsavedChanges.request(TO, FROM);

    expect(decision).not.toBeNull();
    expect(unsavedChanges.prompting).toBe(true);
  });

  it('prevents the native unload while the field is still focused', async () => {
    await typeWithoutLeavingTheField();

    const event = new Event('beforeunload', { cancelable: true });
    window.dispatchEvent(event);

    expect(event.defaultPrevented).toBe(true);
  });

  it('asks before login or logout while the field is still focused', async () => {
    await typeWithoutLeavingTheField();

    void unsavedChanges.confirmDiscard();

    expect(unsavedChanges.prompting).toBe(true);
  });
});
