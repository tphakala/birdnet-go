/**
 * Shared "a play click is never a silent no-op" sequences for the useAudioPlayback
 * and AudioPlayer recovery tests. Each sequence is a list of steps on one fake
 * media element; the caller supplies how a click is made and checked.
 */

import { vi } from 'vitest';
import { MAX_AUDIO_LOAD_RETRIES, AUDIO_RETRY_DELAY_MS } from '$lib/utils/audio';
import { MEDIA_ERR_NETWORK, settle, type FakeMediaController } from './fakeMediaElement';

/** One step of a sequence */
export type SequenceStep =
  | 'click'
  | 'exhaust'
  | 'failLoad'
  | 'advanceRetry'
  | 'completeLoad'
  | 'rejectNextNotAllowed'
  | 'failNetworkWhilePlaying'
  | 'metadataOnly';

/** A named list of steps */
export interface PlaySequence {
  name: string;
  steps: SequenceStep[];
}

/** Sequences after which every click must play, reload, or show a recoverable error. */
export const NEVER_SILENT_SEQUENCES: PlaySequence[] = [
  { name: 'click', steps: ['click'] },
  { name: 'exhaust, click', steps: ['exhaust', 'click'] },
  {
    name: 'exhaust, click, failLoad, advanceRetry, completeLoad, click',
    steps: ['exhaust', 'click', 'failLoad', 'advanceRetry', 'completeLoad', 'click'],
  },
  { name: 'failLoad, click', steps: ['failLoad', 'click'] },
  {
    name: 'failLoad, advanceRetry, rejectNextNotAllowed, click',
    steps: ['failLoad', 'advanceRetry', 'rejectNextNotAllowed', 'click'],
  },
  {
    name: 'click, failNetworkWhilePlaying, advanceRetry, completeLoad, click',
    steps: ['click', 'failNetworkWhilePlaying', 'advanceRetry', 'completeLoad', 'click'],
  },
  { name: 'metadataOnly, click', steps: ['metadataOnly', 'click'] },
  {
    name: 'exhaust, rejectNextNotAllowed, click, click',
    steps: ['exhaust', 'rejectNextNotAllowed', 'click', 'click'],
  },
];

/** Fail every load retry until the budget is spent and the error shows. */
export async function exhaustLoadRetries(ctl: FakeMediaController): Promise<void> {
  for (let i = 0; i < MAX_AUDIO_LOAD_RETRIES; i++) {
    ctl.failLoad();
    await vi.advanceTimersByTimeAsync(AUDIO_RETRY_DELAY_MS);
  }
  ctl.failLoad();
  await settle();
}

/** How a test makes and checks a click, and how it waits out a retry. */
export interface SequenceHandlers {
  /** Press play and assert the click was not a silent no-op */
  click: () => Promise<void>;
  /** Fire the pending retry timer; may also wait out the loading state */
  advanceRetry: () => Promise<void>;
}

/** Run the steps of one sequence against the fake element. */
export async function runPlaySequence(
  steps: SequenceStep[],
  ctl: FakeMediaController,
  handlers: SequenceHandlers
): Promise<void> {
  for (const step of steps) {
    switch (step) {
      case 'click':
        await handlers.click();
        break;
      case 'exhaust':
        await exhaustLoadRetries(ctl);
        break;
      case 'failLoad':
        ctl.failLoad();
        await settle();
        break;
      case 'advanceRetry':
        await handlers.advanceRetry();
        break;
      case 'completeLoad':
        ctl.completeLoad();
        await settle();
        break;
      case 'rejectNextNotAllowed':
        ctl.rejectNextPlay('NotAllowedError');
        break;
      case 'failNetworkWhilePlaying':
        ctl.failLoad(MEDIA_ERR_NETWORK);
        await settle();
        break;
      case 'metadataOnly':
        ctl.completeLoad({ canplay: false });
        await settle();
        break;
    }
  }
}
