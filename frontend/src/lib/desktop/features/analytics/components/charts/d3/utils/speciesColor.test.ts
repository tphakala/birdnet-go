import { describe, it, expect, beforeEach } from 'vitest';
import { getSpeciesColor, registerChart, resetSpeciesColors } from './speciesColor';
import type { ChartTheme } from './theme';

const theme = { background: '#ffffff' } as unknown as ChartTheme; // only background drives the palette

describe('getSpeciesColor', () => {
  beforeEach(() => resetSpeciesColors());

  it('returns a stable color per species regardless of call order', () => {
    const robinFirst = getSpeciesColor('Turdus migratorius', theme);
    getSpeciesColor('Cardinalis cardinalis', theme);
    const robinAgain = getSpeciesColor('Turdus migratorius', theme);
    expect(robinAgain).toBe(robinFirst);
  });

  it('gives distinct colors to distinct species within palette size', () => {
    expect(getSpeciesColor('A', theme)).not.toBe(getSpeciesColor('B', theme));
  });

  it('keeps the map while at least one chart remains registered', () => {
    const unregA = registerChart();
    const unregB = registerChart();
    const aColor = getSpeciesColor('A', theme);
    unregA(); // one chart still registered → map must NOT clear
    expect(getSpeciesColor('A', theme)).toBe(aColor);
    unregB();
  });

  it('clears the map when the last chart unregisters, restarting assignment', () => {
    const unregister = registerChart();
    const first = getSpeciesColor('A', theme); // palette[0]
    getSpeciesColor('B', theme); // palette[1]
    unregister(); // count → 0, map cleared
    // A fresh species now takes palette[0] again, proving the map was cleared.
    expect(getSpeciesColor('C', theme)).toBe(first);
  });
});
