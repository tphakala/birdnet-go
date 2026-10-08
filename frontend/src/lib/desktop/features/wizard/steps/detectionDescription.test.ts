import { describe, it, expect } from 'vitest';
import { readdirSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const MESSAGES_DIR = join(
  dirname(fileURLToPath(import.meta.url)),
  '../../../../../../static/messages'
);
/** A typical stored threshold substituted for the {threshold} placeholder. */
const SAMPLE_THRESHOLD = '0.75';

interface DetectionMessages {
  description: string;
  descriptionStored: string;
}

const locales = readdirSync(MESSAGES_DIR)
  .filter(f => /^[a-z]{2}\.json$/.test(f))
  .map(f => f.replace(/\.json$/, ''));

function detectionMessages(locale: string): DetectionMessages {
  // eslint-disable-next-line security/detect-non-literal-fs-filename -- path built from the repository's own locale list
  const file = JSON.parse(readFileSync(join(MESSAGES_DIR, `${locale}.json`), 'utf-8')) as {
    wizard: { steps: { detection: DetectionMessages } };
  };
  return file.wizard.steps.detection;
}

// The stored-value intro replaces the normal intro in the fixed-height wizard
// step, so it must never be longer than it or the step overflows.
describe('wizard detection intro variants', () => {
  it('covers all 16 locales', () => {
    expect(locales).toHaveLength(16);
  });

  it.each(locales)('%s: the stored-value intro is not longer than the intro', locale => {
    const { description, descriptionStored } = detectionMessages(locale);
    expect(descriptionStored).toContain('{threshold}');
    const rendered = descriptionStored.replace('{threshold}', SAMPLE_THRESHOLD);
    expect(rendered.length).toBeLessThanOrEqual(description.length);
  });
});
