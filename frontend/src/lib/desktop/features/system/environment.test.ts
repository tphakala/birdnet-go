import { describe, it, expect } from 'vitest';
import { isContainerEnvironment } from './environment';

// The inputs below are not invented: they are every string
// internal/sysinfo/environment.go can return as the environment type. Five are
// container runtimes (the Env* constants) and the rest are hosts. Keeping the
// full set here means a new server value shows up as a failing case rather than
// as a silently wrong icon on two pages.
describe('isContainerEnvironment', () => {
  const CONTAINERS = ['Docker', 'Podman', 'LXC', 'systemd-nspawn', 'Container'];
  const HOSTS = [
    'Bare Metal',
    'Native',
    'WSL2',
    'Virtual Machine',
    'KVM',
    'VMware',
    'Hyper-V',
    'VirtualBox',
    'Xen',
    'Parallels',
  ];

  it.each(CONTAINERS)('classifies %s as a container', env => {
    expect(isContainerEnvironment(env)).toBe(true);
  });

  it.each(HOSTS)('classifies %s as a host', env => {
    expect(isContainerEnvironment(env)).toBe(false);
  });

  it('returns false rather than throwing when the field is absent', () => {
    expect(isContainerEnvironment(undefined)).toBe(false);
    expect(isContainerEnvironment('')).toBe(false);
  });

  // The server sends the constants capitalised, so a lowercase value means
  // something upstream transformed it. Treating it as a host is the honest
  // outcome: it makes the mismatch visible instead of quietly guessing.
  it('does not match a lowercased value', () => {
    expect(isContainerEnvironment('docker')).toBe(false);
  });

  // Prefix tolerance is deliberate, so pin it. Neither endpoint appends a
  // sub-type today, but the predicate is written to survive one that does.
  it('matches a value carrying a trailing detail', () => {
    expect(isContainerEnvironment('Docker (rootless)')).toBe(true);
  });

  it('does not match a value that merely contains a runtime name', () => {
    expect(isContainerEnvironment('Not Docker')).toBe(false);
  });
});
