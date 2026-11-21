import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, render } from '@testing-library/svelte';
import { shortcuts } from '$lib/stores/shortcuts.svelte';
import UseShortcutsRemount from './UseShortcutsRemount.test.svelte';

describe('useShortcuts', () => {
  afterEach(() => cleanup());

  it('holds exactly one scope while the owner is mounted, through remounts, and none after', async () => {
    const view = render(UseShortcutsRemount, { props: { generation: 0, mounted: true } });
    expect(shortcuts.scopes).toHaveLength(1);

    for (const generation of [1, 2, 3]) {
      await view.rerender({ generation, mounted: true });
      expect(shortcuts.scopes).toHaveLength(1);
    }

    await view.rerender({ generation: 3, mounted: false });
    expect(shortcuts.scopes).toHaveLength(0);
  });
});
