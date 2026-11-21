import { describe, it, expect } from 'vitest';
import type { AudioSourceConfig, StreamConfig } from '$lib/stores/settings';
import {
  problemSourceNames,
  problemStreamNames,
  soundCardProblems,
  streamProblems,
} from './sourceRules';

function source(name: string, device: string): AudioSourceConfig {
  return { name, device, gain: 0, models: [] };
}

const SAVED = [source('Garden', 'hw:0'), source('Porch', 'hw:1')];

describe('soundCardProblems', () => {
  it('reports nothing for a valid list', () => {
    expect(soundCardProblems(SAVED, SAVED)).toEqual([{}, {}]);
  });

  it.each([
    ['empty', ''],
    ['blank', '   '],
  ])('reports a %s name as required', (_label, name) => {
    const problems = soundCardProblems([source(name, 'hw:0'), SAVED[1]], SAVED);
    expect(problems).toEqual([{ name: 'required' }, {}]);
  });

  it('reports an empty device as required', () => {
    const problems = soundCardProblems([source('Garden', ' '), SAVED[1]], SAVED);
    expect(problems).toEqual([{ device: 'required' }, {}]);
  });

  it('reports a name that now matches another source, ignoring case and spaces, on both rows', () => {
    const problems = soundCardProblems(
      [source('Garden', 'hw:0'), source(' garden ', 'hw:1')],
      SAVED
    );
    expect(problems).toEqual([{ name: 'duplicate' }, { name: 'duplicate' }]);
  });

  it('reports a device that now matches another source on both rows', () => {
    const problems = soundCardProblems([source('Garden', 'hw:0'), source('Porch', 'hw:0')], SAVED);
    expect(problems).toEqual([{ device: 'duplicate' }, { device: 'duplicate' }]);
  });

  it('does not report a duplicate the saved list already holds', () => {
    const saved = [source('Mic', 'hw:0'), source('mic', 'hw:1')];
    expect(soundCardProblems(saved, saved)).toEqual([{}, {}]);
  });

  it('reports the duplicate once it occurs more often than in the saved list', () => {
    const saved = [source('Mic', 'hw:0'), source('mic', 'hw:1')];
    const grown = [...saved, source('MIC', 'hw:2')];
    expect(soundCardProblems(grown, saved)).toEqual([
      { name: 'duplicate' },
      { name: 'duplicate' },
      { name: 'duplicate' },
    ]);
  });

  it('still reports an empty name when the saved list holds one, and keeps the other rows clean', () => {
    const saved = [source('', 'hw:0'), source('Porch', 'hw:1')];
    expect(soundCardProblems(saved, saved)).toEqual([{ name: 'required' }, {}]);
  });

  it('keeps the result independent of row order', () => {
    const edited = [source('Porch', 'hw:1'), source('Garden', 'hw:0'), source('garden', 'hw:2')];
    expect(soundCardProblems(edited, SAVED)).toEqual([
      {},
      { name: 'duplicate' },
      { name: 'duplicate' },
    ]);
  });
});

describe('problemSourceNames', () => {
  const unnamed = (position: number) => `unnamed ${position}`;

  it('names each source with a problem once, in list order', () => {
    const sources = [source('Mic', 'hw:0'), source('Porch', 'hw:1'), source('Mic', 'hw:2')];
    const problems = soundCardProblems(sources, SAVED);

    expect(problems[0]).toEqual({ name: 'duplicate' });
    expect(problems[2]).toEqual({ name: 'duplicate' });
    expect(problemSourceNames(sources, problems, unnamed)).toEqual(['Mic']);
  });

  it('labels a source without a name by its position', () => {
    const sources = [source('Garden', 'hw:0'), source('  ', 'hw:1')];

    expect(problemSourceNames(sources, soundCardProblems(sources, SAVED), unnamed)).toEqual([
      'unnamed 2',
    ]);
  });

  it('lists nothing when no source has a problem', () => {
    expect(problemSourceNames(SAVED, soundCardProblems(SAVED, SAVED), unnamed)).toEqual([]);
  });
});

function stream(name: string, url: string, type: StreamConfig['type'] = 'rtsp'): StreamConfig {
  return { name, url, type, enabled: true, models: [] };
}

const SAVED_STREAMS = [stream('Front', 'rtsp://front/live'), stream('Back', 'rtsp://back/live')];
const NO_TESTS: ReadonlySet<string> = new Set();

describe('streamProblems', () => {
  it('reports nothing for the saved list, which needs no test', () => {
    expect(streamProblems(SAVED_STREAMS, SAVED_STREAMS, NO_TESTS)).toEqual([{}, {}]);
  });

  it.each([
    ['empty', ''],
    ['blank', '   '],
  ])('reports a %s name as required', (_label, name) => {
    const problems = streamProblems(
      [stream(name, 'rtsp://front/live'), SAVED_STREAMS[1]],
      SAVED_STREAMS,
      NO_TESTS
    );
    expect(problems).toEqual([{ name: 'required' }, {}]);
  });

  it('reports a name longer than the server accepts, counted in bytes', () => {
    // 22 euro signs are 22 characters but 66 bytes.
    const problems = streamProblems(
      [stream('\u20ac'.repeat(22), 'rtsp://front/live'), SAVED_STREAMS[1]],
      SAVED_STREAMS,
      NO_TESTS
    );
    expect(problems[0]).toEqual({ name: 'tooLong' });
  });

  it('reports a name that now matches another stream, ignoring case and spaces, on both rows', () => {
    const problems = streamProblems(
      [stream('Front', 'rtsp://front/live'), stream(' front ', 'rtsp://back/live')],
      SAVED_STREAMS,
      NO_TESTS
    );
    expect(problems).toEqual([{ name: 'duplicate' }, { name: 'duplicate' }]);
  });

  it('reports a URL that now matches another stream on both rows', () => {
    const problems = streamProblems(
      [stream('Front', 'rtsp://front/live'), stream('Back', ' rtsp://front/live ')],
      SAVED_STREAMS,
      NO_TESTS
    );
    expect(problems).toEqual([{ url: 'duplicate' }, { url: 'duplicate' }]);
  });

  it('does not report a duplicate the saved list already holds', () => {
    const saved = [stream('Cam', 'rtsp://a/live'), stream('cam', 'rtsp://b/live')];
    expect(streamProblems(saved, saved, NO_TESTS)).toEqual([{}, {}]);
  });

  it('reports an empty URL as required and one that does not fit its type as invalid', () => {
    const problems = streamProblems(
      [stream('Front', ' '), stream('Back', 'rtsp://back/live', 'http')],
      SAVED_STREAMS,
      NO_TESTS
    );
    expect(problems).toEqual([{ url: 'required' }, { url: 'invalid' }]);
  });

  it('does not report a saved URL that no longer fits its type, so a stored oddity cannot block Save', () => {
    const saved = [stream('Odd', 'rtsp://odd/live', 'http')];
    expect(streamProblems(saved, saved, NO_TESTS)).toEqual([{}]);
  });

  it('asks for a test of a URL the saved list does not have, and accepts it once tested', () => {
    const edited = [stream('Front', 'rtsp://front/new'), SAVED_STREAMS[1]];

    expect(streamProblems(edited, SAVED_STREAMS, NO_TESTS)).toEqual([{ url: 'untested' }, {}]);
    expect(streamProblems(edited, SAVED_STREAMS, new Set(['rtsp://front/new']))).toEqual([{}, {}]);
  });

  it('keys the test to the URL: editing again asks again, and going back to the saved URL does not', () => {
    const tested = new Set(['rtsp://front/new']);
    const edited = [stream('Front', 'rtsp://front/new2'), SAVED_STREAMS[1]];
    const reverted = [stream('Front', 'rtsp://front/live'), SAVED_STREAMS[1]];

    expect(streamProblems(edited, SAVED_STREAMS, tested)).toEqual([{ url: 'untested' }, {}]);
    expect(streamProblems(reverted, SAVED_STREAMS, tested)).toEqual([{}, {}]);
  });

  it('asks for no test when two saved streams swap URLs', () => {
    const swapped = [stream('Front', 'rtsp://back/live'), stream('Back', 'rtsp://front/live')];
    expect(streamProblems(swapped, SAVED_STREAMS, NO_TESTS)).toEqual([{}, {}]);
  });

  it('keeps the test with the URL when a row above the edited one is deleted', () => {
    const tested = new Set(['rtsp://back/new']);
    const edited = [stream('Front', 'rtsp://front/live'), stream('Back', 'rtsp://back/new')];
    const withoutFirst = edited.slice(1);

    expect(streamProblems(edited, SAVED_STREAMS, tested)).toEqual([{}, {}]);
    expect(streamProblems(withoutFirst, SAVED_STREAMS, tested)).toEqual([{}]);
    expect(streamProblems(withoutFirst, SAVED_STREAMS, NO_TESTS)).toEqual([{ url: 'untested' }]);
  });

  it('reports a name and an untested URL of the same stream together', () => {
    const problems = streamProblems([stream('', 'rtsp://new/live')], SAVED_STREAMS, NO_TESTS);
    expect(problems).toEqual([{ name: 'required', url: 'untested' }]);
  });
});

describe('problemStreamNames', () => {
  const unnamed = (position: number) => `unnamed ${position}`;
  const names = (
    streams: StreamConfig[],
    kind: 'invalid' | 'untested',
    tested: ReadonlySet<string> = NO_TESTS
  ) => problemStreamNames(streams, streamProblems(streams, SAVED_STREAMS, tested), unnamed, kind);

  it('lists the streams with a name or URL problem once each, in list order', () => {
    const streams = [
      stream('Cam', 'rtsp://a/live'),
      stream('Cam', 'rtsp://a/live'),
      stream('Back', 'rtsp://back/live', 'http'),
    ];

    expect(names(streams, 'invalid')).toEqual(['Cam', 'Back']);
  });

  it('lists a stream with an untested URL only as untested', () => {
    const streams = [SAVED_STREAMS[0], stream('Back', 'rtsp://back/new')];

    expect(names(streams, 'invalid')).toEqual([]);
    expect(names(streams, 'untested')).toEqual(['Back']);
    expect(names(streams, 'untested', new Set(['rtsp://back/new']))).toEqual([]);
  });

  it('names a stream with a bad name and an untested URL once, as invalid', () => {
    const streams = [stream('', 'rtsp://new/live')];

    expect(names(streams, 'invalid')).toEqual(['unnamed 1']);
    expect(names(streams, 'untested')).toEqual([]);
  });
});
