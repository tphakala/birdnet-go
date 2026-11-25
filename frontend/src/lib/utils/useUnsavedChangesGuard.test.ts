import { afterEach, describe, expect, it } from 'vitest';
import { render } from '@testing-library/svelte';
import { unsavedChanges, type NavigationTarget } from '$lib/stores/unsavedChanges.svelte';
import Host from './useUnsavedChangesGuard.test.svelte';

const FROM: NavigationTarget = { path: ['', 'ui', 'a'].join('/'), search: '' };
const TO: NavigationTarget = { path: ['', 'ui', 'b'].join('/'), search: '' };

describe('useUnsavedChangesGuard', () => {
  afterEach(() => {
    unsavedChanges.resetForTesting();
  });

  it('guards while the component is mounted and reads its dirty state at each decision', async () => {
    const view = render(Host, { props: { dirty: true } });
    expect(unsavedChanges.blocks(TO, FROM)).toBe(true);

    await view.rerender({ dirty: false });

    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);
  });

  it('removes the registration when the component unmounts', () => {
    const view = render(Host, { props: { dirty: true } });
    expect(unsavedChanges.blocks(TO, FROM)).toBe(true);

    view.unmount();

    expect(unsavedChanges.blocks(TO, FROM)).toBe(false);
  });
});
