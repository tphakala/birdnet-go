import { afterEach, describe, expect, it, vi } from 'vitest';
import { downloadFromUrl } from './fileHelpers';

describe('downloadFromUrl', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('clicks a download anchor for the url and removes it again', () => {
    const clicked: Array<{ href: string | null; download: boolean }> = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (
      this: HTMLAnchorElement
    ) {
      clicked.push({ href: this.getAttribute('href'), download: this.hasAttribute('download') });
    });

    downloadFromUrl('/api/v2/support/download/dump-1');

    expect(clicked).toEqual([{ href: '/api/v2/support/download/dump-1', download: true }]);
    expect(document.querySelector('a[download]')).toBeNull();
  });

  it('ignores an empty url, which would resolve to the current page', () => {
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {});

    downloadFromUrl('');

    expect(click).not.toHaveBeenCalled();
  });
});
