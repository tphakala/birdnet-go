import { describe, it, expect, afterEach } from 'vitest';
import { portal } from './portal';

describe('portal action', () => {
  afterEach(() => {
    document.body.innerHTML = '';
  });

  it('re-parents the node to document.body by default', () => {
    const parent = document.createElement('div');
    const node = document.createElement('span');
    parent.appendChild(node);
    document.body.appendChild(parent);

    portal(node);

    expect(node.parentNode).toBe(document.body);
    expect(parent.contains(node)).toBe(false);
  });

  it('removes the node from the DOM on destroy', () => {
    const node = document.createElement('div');
    const action = portal(node);

    expect(document.body.contains(node)).toBe(true);

    action.destroy();

    expect(document.body.contains(node)).toBe(false);
  });

  it('re-parents the node to an explicit target element', () => {
    const target = document.createElement('section');
    document.body.appendChild(target);
    const node = document.createElement('div');

    const action = portal(node, target);

    expect(node.parentNode).toBe(target);

    action.destroy();

    expect(target.contains(node)).toBe(false);
  });

  it('does not remove the node on destroy if it was re-parented elsewhere', () => {
    const target = document.createElement('section');
    const other = document.createElement('aside');
    document.body.append(target, other);
    const node = document.createElement('div');

    const action = portal(node, target);
    // Simulate the node being moved to a different container after mount.
    other.appendChild(node);

    expect(() => action.destroy()).not.toThrow();
    // The guard only removes when still attached to the original target,
    // so the relocated node is left untouched.
    expect(other.contains(node)).toBe(true);
  });
});
