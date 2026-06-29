import { describe, it, expect } from 'vitest';

import { chartsForGroup } from './charts';

// Guards the IA redesign retag: the nocturnal-clock chart moved out of the
// 'patterns' group into its own 'nocturnal' group/page. A future accidental
// revert (or a stray second chart landing in either group) trips these.
describe('chart group membership', () => {
  it('places nocturnal-clock in the nocturnal group', () => {
    const ids = chartsForGroup('nocturnal').map(c => c.id);
    expect(ids).toContain('nocturnal-clock');
  });

  it('removes nocturnal-clock from the patterns group, leaving exactly five charts', () => {
    const ids = chartsForGroup('patterns').map(c => c.id);
    expect(ids).not.toContain('nocturnal-clock');
    expect(ids).toEqual([
      'seasonal-heatmap',
      'species-ridgeline',
      'acoustic-succession',
      'dawn-chorus-onset',
      'time-of-day-species',
    ]);
  });
});
