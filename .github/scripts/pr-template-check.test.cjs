'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const { describe, it } = require('node:test');

const {
  COMMENT_AUTHOR,
  COMMENT_MARKER,
  CONSENTED_LABEL,
  EXEMPT_LABEL,
  NEEDS_TEMPLATE_LABEL,
  TEMPLATE_PATH,
  checkPullRequest,
  parseSections,
  renderComment,
  run,
  stripComments,
} = require('./pr-template-check.cjs');

const template = fs.readFileSync(TEMPLATE_PATH, 'utf8');

/** Fills in the real template the way a contributor would. */
function filledTemplate({ related = 'Closes #123', tick = () => true } = {}) {
  return template
    .replace('<!-- What does this PR change, and why? -->', 'Fixes the audio player freeze.')
    .replace('<!-- e.g. Closes #123 -->', related)
    .split('\n')
    .map(line => (line.startsWith('- [ ] ') && tick(line) ? line.replace('- [ ]', '- [x]') : line))
    .join('\n');
}

const untickFeatureBox = line => !line.includes('Feature PRs only');

const boxLine = phrase =>
  template.split('\n').find(line => line.startsWith('- [ ]') && line.includes(phrase));
const consentLine = boxLine('#4243');
const tickedConsent = consentLine.replace('- [ ]', '- [x]');
const tickedContributing = boxLine('Contributing Guidelines').replace('- [ ]', '- [x]');
const CONSENT_MISSING =
  'The relicensing agreement is missing from **Licensing**. Copy it from the template and tick it. The pull request cannot be merged without it.';
// Upper bound for the linear-time tests; the code under test takes about 1 ms.
const LINEAR_TIME_BUDGET_MS = 500;

// A description in the shape tools write by default, ignoring the template.
const ownFormatBody = `## Overview

Adds a new local weather provider.

## Implementation

- UDP listener

## Related

Addresses #4144.
`;

describe('checkPullRequest', () => {
  it('accepts a filled in template on a fix', () => {
    const body = filledTemplate({ tick: untickFeatureBox });
    assert.deepEqual(
      checkPullRequest({ title: 'fix(audio): stop the freeze', body, template }),
      []
    );
  });

  it('accepts a filled in template on a feature that links its discussion', () => {
    const body = filledTemplate();
    assert.deepEqual(
      checkPullRequest({ title: 'feat(weather): add a provider', body, template }),
      []
    );
  });

  it('accepts a discussion link as the related issue of a feature', () => {
    const body = filledTemplate({
      related: 'Agreed in https://github.com/tphakala/birdnet-go/discussions/42',
    });
    assert.deepEqual(checkPullRequest({ title: 'feat: add a provider', body, template }), []);
  });

  it('accepts a cross-repository reference as the related issue of a feature', () => {
    const body = filledTemplate({ related: 'Agreed in tphakala/birdnet-go#4144' });
    assert.deepEqual(checkPullRequest({ title: 'feat: add a provider', body, template }), []);
  });

  it('does not take a URL fragment as a linked issue', () => {
    for (const related of ['See https://example.com/docs#2', 'See example.com/docs#2']) {
      const body = filledTemplate({ related });
      const problems = checkPullRequest({ title: 'feat: add a provider', body, template });
      assert.equal(problems.length, 1, related);
      assert.match(problems[0], /must link the issue or discussion/);
    }
  });

  it('accepts owner/repo#N wherever it starts', () => {
    for (const related of [
      'tphakala/birdnet-go#4144',
      'Agreed in the discussion.\ntphakala/birdnet-go#4144',
      'Closes:tphakala/birdnet-go#4144',
    ]) {
      const body = filledTemplate({ related });
      assert.deepEqual(checkPullRequest({ title: 'feat: x', body, template }), [], related);
    }
  });

  it('checks issue references in linear time', () => {
    const related = `${'a'.repeat(100000)} ${'/a'.repeat(1500)} ${'github.com/a'.repeat(200)}`;
    const body = filledTemplate({ related });
    const started = Date.now();
    checkPullRequest({ title: 'feat: add a provider', body, template });
    assert.ok(Date.now() - started < LINEAR_TIME_BUDGET_MS, `took ${Date.now() - started} ms`);
  });

  it('matches a feat title at the start in any case', () => {
    const body = filledTemplate({ tick: untickFeatureBox });
    assert.equal(checkPullRequest({ title: 'Feat(ui): x', body, template }).length, 1);
    assert.deepEqual(checkPullRequest({ title: 'fix: feat: x', body, template }), []);
  });

  it('accepts upper case X and Windows line endings', () => {
    const body = filledTemplate({ tick: untickFeatureBox })
      .replaceAll('- [x]', '- [X]')
      .replaceAll('\n', '\r\n');
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });

  it('rejects a description in its own format', () => {
    const problems = checkPullRequest({
      title: 'feat(weather): add Tempest WeatherFlow local UDP provider',
      body: ownFormatBody,
      template,
    });
    for (const section of ['Description', 'Related issue', 'Checklist', 'Licensing (required)']) {
      assert.ok(
        problems.includes(`The **${section}** section is missing.`),
        `expected the ${section} section to be reported, got ${JSON.stringify(problems)}`
      );
    }
    assert.ok(problems.some(p => p.startsWith('The relicensing agreement')));
    assert.ok(problems.some(p => p.includes('"Feature PRs only" checkbox is missing')));
  });

  it('rejects the untouched template', () => {
    const problems = checkPullRequest({ title: 'fix: something', body: template, template });
    assert.ok(problems.some(p => p.startsWith('The **Description** section is empty')));
    assert.ok(problems.some(p => p.startsWith('The **Related issue** section is empty')));
    assert.ok(problems.some(p => p.includes('Contributing Guidelines checkbox under')));
    assert.ok(
      problems.some(p => p.includes('relicensing agreement under **Licensing** is not ticked'))
    );
  });

  it('does not tell authors a checkbox section may be skipped', () => {
    const body =
      '## Description\n\nx\n\n## Related issue\n\n\n## Checklist\n\n## Licensing (required)\n';
    const problems = checkPullRequest({ title: 'fix: x', body, template });
    assert.ok(
      problems.includes(
        'The **Related issue** section is empty. If it does not apply, say so in a sentence.'
      )
    );
    for (const name of ['Checklist', 'Licensing (required)']) {
      assert.ok(
        problems.includes(
          `The **${name}** section is empty. Copy its checkboxes from the template and tick them.`
        ),
        `expected the ${name} message, got ${JSON.stringify(problems)}`
      );
    }
  });

  it('rejects an empty body', () => {
    const problems = checkPullRequest({ title: 'fix: something', body: null, template });
    assert.ok(problems.length > 0);
    assert.ok(problems.some(p => p.startsWith('The relicensing agreement')));
  });

  it('does not accept ticked boxes inside a code block', () => {
    const body = filledTemplate({ tick: untickFeatureBox })
      .replace(tickedContributing, '')
      .replace(tickedConsent, '')
      .replace(
        'Fixes the audio player freeze.',
        `Fixes the audio player freeze.\n\n\`\`\`\n${tickedContributing}\n${tickedConsent}\n\`\`\``
      );
    const problems = checkPullRequest({ title: 'fix: something', body, template });
    assert.deepEqual(problems, [
      'The Contributing Guidelines checkbox is missing from **Checklist**. Copy it from the template and tick it.',
      CONSENT_MISSING,
    ]);
  });

  it('only accepts the consent box in the Licensing section', () => {
    const body = filledTemplate({ tick: untickFeatureBox })
      .replace(tickedConsent, '')
      .replace(
        'Fixes the audio player freeze.',
        `Fixes the audio player freeze.\n\n${tickedConsent}`
      );
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), [CONSENT_MISSING]);
  });

  describe('a line right after a box, which GitHub renders as part of its text', () => {
    const reworded =
      "The relicensing agreement under **Licensing** does not match the template's wording. Copy it unchanged from the template and tick it. The pull request cannot be merged without it.";
    const withNextLine = next =>
      filledTemplate({ tick: untickFeatureBox }).replace(
        tickedConsent,
        `${tickedConsent}\n${next}`
      );
    const check = body => checkPullRequest({ title: 'fix: x', body, template });

    for (const [name, next] of [
      ['a reservation', 'Except that I do not grant the relicensing right.'],
      ['an indented reservation', '  except for the files under docs/'],
      ['an indented setext underline', '  ---'],
      ['a nested bullet', '  - except for the files under docs/'],
      ['a nested numbered item', '  1. except for the files under docs/'],
      ['an indented heading', '  # except for docs'],
      ['an indented fence', '  ```\n  except for docs\n  ```'],
      ['an indented paragraph after a blank line', '\n  Except for the files under docs/'],
      ['an inline HTML caveat', '<sub>except for docs</sub>'],
      ['an autolink caveat', '<https://example.com> except for docs'],
      ['a tab indented paragraph after a blank line', '\n\tExcept for docs'],
    ]) {
      it(`counts ${name} as part of the box`, () => {
        assert.deepEqual(check(withNextLine(next)), [reworded]);
      });
    }

    for (const [name, next] of [
      ['a blank line and then text', '\nThanks for reviewing.'],
      ['another list item', '- a note'],
      ['a heading', '## Notes\ntext'],
      ['a lower level heading', '### Notes'],
      ['a fence', '```\ncode\n```'],
      ['a block quote', '> a quote'],
      ['a thematic break', '***'],
      ['an HTML block', '<details>\n<summary>Logs</summary>\n</details>'],
      ['a whitespace-only line', '   \nThanks for reviewing.'],
      ['a fence, for text after it', '```\ncode\n```\nThanks for reviewing.'],
      ['an unindented paragraph, for indented text after it', '\nThanks\n  more'],
      ['a blank line, for text indented one space', '\n Thanks for reviewing.'],
      ['an ordered list item', '2. a note'],
    ]) {
      it(`ends the box at ${name}`, () => {
        assert.deepEqual(check(withNextLine(next)), []);
      });
    }
  });

  it('keeps the description after an unclosed comment that does not start a line', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      'Fixes the audio player freeze.',
      'Fixes the audio player freeze. Svelte comments open with <!-- in markup.'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
  });

  it('handles many unclosed comments on one line in linear time', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      'Fixes the audio player freeze.',
      `Fixes it. x${'<!--'.repeat(16000)}`
    );
    const started = Date.now();
    checkPullRequest({ title: 'fix: x', body, template });
    assert.ok(Date.now() - started < LINEAR_TIME_BUDGET_MS, `took ${Date.now() - started} ms`);
  });

  it('strips comments the way GitHub hides them', () => {
    for (const [input, expected] of [
      ['a <!-- b --> c', 'a  c'],
      ['a<!---->b', 'ab'],
      ['a<!-->b', 'ab'],
      ['a<!--->b', 'ab'],
      ['a <!-- b\nc --> d', 'a  d'],
      ['a <!-- b', 'a <!-- b'],
      ['a <!-- b\n<!-- c', 'a <!-- b\n'],
      ['x\n<!-- open\nrest', 'x\n'],
      ['x\n   <!-- open\nrest', 'x\n   '],
      ['x\n    <!-- open\nrest', 'x\n    <!-- open\nrest'],
      ['x\r\n<!-- open\r\nrest', 'x\r\n'],
      ['<!-- open\nrest', ''],
    ]) {
      assert.equal(stripComments(input), expected, JSON.stringify(input));
    }
  });

  it('reads a long fence line ending in a line separator in linear time', () => {
    const body = `## Description\n\n${'`'.repeat(65000)}\u2028\n`;
    const started = Date.now();
    checkPullRequest({ title: 'fix: x', body, template });
    assert.ok(Date.now() - started < LINEAR_TIME_BUDGET_MS, `took ${Date.now() - started} ms`);
  });

  it('ignores a checkbox before the first section', () => {
    const body = `- [x] stray\n\n${filledTemplate({ tick: untickFeatureBox })}`;
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
  });

  it('collects a box with many continuation lines in linear time', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      tickedConsent,
      `${tickedConsent}\n${'a\n'.repeat(30000)}`
    );
    const started = Date.now();
    checkPullRequest({ title: 'fix: x', body, template });
    assert.ok(Date.now() - started < LINEAR_TIME_BUDGET_MS, `took ${Date.now() - started} ms`);
  });

  it('accepts a box with extra spaces inside and after it', () => {
    const spaced = `${tickedConsent.replace(' I agree ', '  I   agree ')} \t`;
    const body = filledTemplate({ tick: untickFeatureBox }).replace(tickedConsent, spaced);
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
  });

  it('does not accept a reworded relicensing box as consent', () => {
    const body = filledTemplate({ tick: untickFeatureBox })
      .split('\n')
      .map(line =>
        line.includes('#4243') && line.startsWith('- [x]')
          ? '- [x] I do not agree to relicense my work.'
          : line
      )
      .join('\n');
    const problems = checkPullRequest({ title: 'fix: something', body, template });
    assert.deepEqual(problems, [
      "The relicensing agreement under **Licensing** does not match the template's wording. Copy it unchanged from the template and tick it. The pull request cannot be merged without it.",
    ]);
  });

  it('reports a reworded relicensing box that says relicensing', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      tickedConsent,
      '- [x] My contribution carries the relicensing grant from the contributing guidelines.'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), [
      "The relicensing agreement under **Licensing** does not match the template's wording. Copy it unchanged from the template and tick it. The pull request cannot be merged without it.",
    ]);
  });

  it('is not misled by other checkboxes that mention the same words', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      'Fixes the audio player freeze.',
      'Fixes the audio player freeze.\n\n- [ ] relicense the docs later\n- [ ] read the Contributing Guidelines again'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });

  describe('a consent line that GitHub does not render as a ticked checkbox', () => {
    const licensing = '## Licensing (required)';
    // The real box removed, with the variant inserted under the heading. The
    // variant is the only candidate, so it decides the result.
    const withLicensing = section =>
      filledTemplate({ tick: untickFeatureBox })
        .replace(tickedConsent, '')
        .replace(`${licensing}\n`, `${licensing}\n\n${section}\n`);
    const check = body => checkPullRequest({ title: 'fix: x', body, template });

    it('is consent as a plain ticked line (control for the cases below)', () => {
      assert.deepEqual(check(withLicensing(tickedConsent)), []);
    });

    for (const [name, wrapped] of [
      ['inside a tilde fence that contains a backtick fence', `~~~\n\`\`\`\n${tickedConsent}\n~~~`],
      [
        'inside a backtick fence that contains a tilde line',
        `\`\`\`\n~~~\n${tickedConsent}\n\`\`\``,
      ],
      [
        'inside a longer fence that contains a shorter one',
        `\`\`\`\`\n\`\`\`\n${tickedConsent}\n\`\`\`\n\`\`\`\``,
      ],
      [
        'inside a fence whose closing line has text after it',
        `\`\`\`\n\`\`\` x\n${tickedConsent}\n\`\`\``,
      ],
      ['in an indented code block', `text\n\n    ${tickedConsent}`],
      ['after an unclosed HTML comment', `<!--\n${tickedConsent}`],
    ]) {
      it(`is not consent ${name}`, () => {
        assert.ok(check(withLicensing(wrapped)).includes(CONSENT_MISSING));
      });
    }

    it('is not consent when another copy is unticked', () => {
      const body = filledTemplate({ tick: untickFeatureBox }).replace(
        tickedConsent,
        `${tickedConsent}\n${consentLine}`
      );
      assert.deepEqual(check(body), [
        'The relicensing agreement under **Licensing** is not ticked. The pull request cannot be merged without it.',
      ]);
    });

    it('is consent in a fence opened with an info string', () => {
      const body = filledTemplate({ tick: untickFeatureBox }).replace(
        'Fixes the audio player freeze.',
        'Fixes the audio player freeze.\n\n```js\nconst x = 1;\n```'
      );
      assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
    });
  });

  it('merges a repeated section instead of replacing it', () => {
    const body = `${filledTemplate({ tick: untickFeatureBox })}\n## Licensing\n\nnothing more\n`;
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
  });

  it('reports a reworded Contributing Guidelines box', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      '- [x] I have read the [Contributing Guidelines]',
      '- [x] I skimmed the [Contributing Guidelines]'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), [
      "The Contributing Guidelines checkbox under **Checklist** does not match the template's wording. Copy it unchanged from the template and tick it.",
    ]);
  });

  it('is not misled by a decoy box in the same section', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      '## Licensing (required)\n',
      '## Licensing (required)\n\n- [ ] relicense the docs later\n'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: x', body, template }), []);
  });

  it('fails loudly when the template loses a required section', () => {
    const noLicensing = template.slice(0, template.indexOf('## Licensing'));
    assert.throws(
      () => checkPullRequest({ title: 'fix: x', body: filledTemplate(), template: noLicensing }),
      /under "licensing"/
    );
  });

  it('fails loudly when the template loses a required checkbox', () => {
    const reworded = template.replace(consentLine, '- [ ] I agree.');
    assert.throws(
      () => checkPullRequest({ title: 'fix: x', body: filledTemplate(), template: reworded }),
      /no checkbox matching \/relicens\/i under "licensing"/
    );
  });

  it('accepts consent recorded with the relicense: consented label instead of the box', () => {
    const body = filledTemplate({
      tick: line => !line.includes('#4243') && untickFeatureBox(line),
    });
    const check = labels => checkPullRequest({ title: 'fix: x', body, template, labels });
    assert.deepEqual(check([CONSENTED_LABEL]), []);
    assert.equal(check([]).length, 1);
    assert.equal(check(['relicense: pending']).length, 1);
  });

  it('keeps the other requirements when consent is recorded', () => {
    const problems = checkPullRequest({
      title: 'feat: x',
      body: ownFormatBody,
      template,
      labels: [CONSENTED_LABEL],
    });
    assert.ok(problems.includes('The **Licensing (required)** section is missing.'));
    assert.ok(problems.some(p => p.startsWith('The Contributing Guidelines checkbox')));
    assert.ok(problems.some(p => p.includes('"Feature PRs only" checkbox')));
    assert.ok(!problems.some(p => p.startsWith('The relicensing agreement')));
    const reworded = template.replace(consentLine, '- [ ] I agree.');
    assert.throws(() =>
      checkPullRequest({ title: 'fix: x', body: '', template: reworded, labels: [CONSENTED_LABEL] })
    );
  });

  it('rejects a feature without the feature box ticked', () => {
    const body = filledTemplate({ tick: untickFeatureBox });
    const problems = checkPullRequest({ title: 'feat!: breaking feature', body, template });
    assert.deepEqual(problems, [
      'This is a feature pull request, but the "Feature PRs only" checkbox under **Checklist** is not ticked.',
    ]);
  });

  it('rejects a feature without a linked issue or discussion', () => {
    const body = filledTemplate({ related: 'No related issue' });
    const problems = checkPullRequest({ title: 'feat(ui): add a page', body, template });
    assert.equal(problems.length, 1);
    assert.match(problems[0], /must link the issue or discussion/);
  });

  it('does not take the template example in a comment as a link', () => {
    const body = filledTemplate({ related: '<!-- e.g. Closes #123 --> none' });
    const problems = checkPullRequest({ title: 'feat(ui): add a page', body, template });
    assert.equal(problems.length, 1);
    assert.match(problems[0], /must link the issue or discussion/);
  });

  it('parses long heading lines in linear time', () => {
    const lineLength = 3000;
    const body = [`## a${' '.repeat(lineLength)}x`, `## b${'('.repeat(lineLength)}`].join('\n');
    const started = Date.now();
    checkPullRequest({ title: 'fix: something', body, template });
    assert.ok(Date.now() - started < LINEAR_TIME_BUDGET_MS, `took ${Date.now() - started} ms`);
  });

  it('reads ATX closing hashes the way GitHub renders them', () => {
    const fill = filledTemplate({ tick: untickFeatureBox });
    const closed = fill.replace('## Description', '## Description ##');
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body: closed, template }), []);
    // A hash run with no space before it is part of the heading text.
    const glued = fill.replace('## Description', '## Description#');
    assert.ok(
      checkPullRequest({ title: 'fix: something', body: glued, template }).includes(
        'The **Description** section is missing.'
      )
    );
  });

  it('parses heading edge cases', () => {
    const keys = body => [...parseSections(body).keys()];
    // A tab after ##, a tab before the closing run, a stray carriage return.
    assert.deepEqual(keys('##\tNotes'), ['notes']);
    assert.deepEqual(keys('## Notes\t##  '), ['notes']);
    assert.deepEqual(keys('## Notes\r\r\ntext'), ['notes']);
    // A heading that is only a closing run has empty text, not "##".
    assert.deepEqual(keys('## ##'), ['']);
    // Only a trailing note after other text is dropped from the key.
    assert.deepEqual(keys('## (note)'), ['(note)']);
    assert.deepEqual(keys('## Notes)'), ['notes)']);
    assert.deepEqual(keys('## Notes (a) (b)'), ['notes (a)']);
  });

  it('matches a heading without the note in parentheses', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      '## Licensing (required)',
      '## Licensing'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });

  it('ignores headings inside code blocks', () => {
    const body = `\`\`\`\n${filledTemplate({ tick: untickFeatureBox })}\n\`\`\`\n`;
    const problems = checkPullRequest({ title: 'fix: something', body, template });
    for (const section of ['Description', 'Related issue', 'Checklist', 'Licensing (required)']) {
      assert.ok(problems.includes(`The **${section}** section is missing.`), section);
    }
  });

  it('does not require a linked issue on a fix', () => {
    const body = filledTemplate({ related: 'No related issue', tick: untickFeatureBox });
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });
});

/** A fake of the parts of the github-script environment that run() uses. */
function fakeEnvironment({ title, body, labels = [], comments = [], failures = {} }) {
  const calls = [];
  const record = name => async params => {
    calls.push({ name, params });
    if (failures[name]) {
      throw Object.assign(new Error(`HTTP ${failures[name]}`), { status: failures[name] });
    }
    return {};
  };
  const github = {
    paginate: async (route, params) => {
      assert.equal(route, github.rest.issues.listComments);
      assert.equal(params.issue_number, 7);
      return comments;
    },
    rest: {
      issues: {
        listComments: record('listComments'),
        addLabels: record('addLabels'),
        removeLabel: record('removeLabel'),
        createComment: record('createComment'),
        updateComment: record('updateComment'),
        deleteComment: record('deleteComment'),
      },
    },
  };
  const context = {
    repo: { owner: 'tphakala', repo: 'birdnet-go' },
    payload: { pull_request: { number: 7, title, body, labels: labels.map(name => ({ name })) } },
  };
  const core = {
    failed: null,
    info: () => {},
    setFailed(message) {
      this.failed = message;
    },
  };
  return { github, context, core, calls };
}

const botComment = body => ({ id: 99, user: { login: COMMENT_AUTHOR }, body });

describe('run', () => {
  it('finds its comment by the account GITHUB_TOKEN posts as', () => {
    assert.equal(COMMENT_AUTHOR, 'github-actions[bot]');
  });

  it('labels, comments and fails when the description does not follow the template', async () => {
    const env = fakeEnvironment({ title: 'feat: x', body: ownFormatBody });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'createComment']
    );
    assert.deepEqual(env.calls[0].params.labels, [NEEDS_TEMPLATE_LABEL]);
    assert.ok(env.calls[1].params.body.startsWith(COMMENT_MARKER));
    const problems = checkPullRequest({ title: 'feat: x', body: ownFormatBody, template });
    assert.ok(problems.length > 0);
    assert.ok(env.core.failed);
    for (const problem of problems) {
      assert.ok(env.calls[1].params.body.includes(`\n- ${problem}`), problem);
      assert.ok(env.core.failed.includes(`\n- ${problem}`), problem);
    }
    assert.ok(env.core.failed);
  });

  it('updates its own comment instead of posting another one', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      labels: [NEEDS_TEMPLATE_LABEL],
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'updateComment']
    );
    assert.equal(env.calls[1].params.comment_id, 99);
  });

  it('leaves an up to date comment alone', async () => {
    const problems = checkPullRequest({ title: 'feat: x', body: ownFormatBody, template });
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      labels: [NEEDS_TEMPLATE_LABEL],
      comments: [botComment(renderComment(problems))],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels']
    );
    assert.ok(env.core.failed);
  });

  it('does not touch a marker comment someone else posted', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      labels: [NEEDS_TEMPLATE_LABEL],
      comments: [{ id: 5, user: { login: 'someone' }, body: COMMENT_MARKER }],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'createComment']
    );
  });

  it('removes the label and the comment once the description is fixed', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: filledTemplate(),
      labels: [NEEDS_TEMPLATE_LABEL],
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['removeLabel', 'deleteComment']
    );
    assert.equal(env.calls[1].params.comment_id, 99);
    assert.equal(env.core.failed, null);
  });

  it('passes the pull request labels to the check', async () => {
    const body = filledTemplate({ tick: line => !line.includes('#4243') });
    const env = fakeEnvironment({ title: 'feat: x', body, labels: [CONSENTED_LABEL] });
    await run(env);
    assert.equal(env.core.failed, null);
  });

  it('leaves other comments by the same account alone', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      comments: [botComment('i18n validation results')],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'createComment']
    );
  });

  it('removes the label even when the event payload does not show it yet', async () => {
    const env = fakeEnvironment({ title: 'feat: x', body: filledTemplate() });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['removeLabel']
    );
    assert.equal(env.calls[0].params.name, NEEDS_TEMPLATE_LABEL);
  });

  it('ignores a label that is already gone', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: filledTemplate(),
      failures: { removeLabel: 404 },
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['removeLabel']
    );
    assert.equal(env.core.failed, null);
  });

  it('propagates other errors from removing the label', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: filledTemplate(),
      failures: { removeLabel: 500 },
    });
    await assert.rejects(run(env), { status: 500 });
  });

  it('ignores a comment that is already deleted', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: filledTemplate(),
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
      failures: { deleteComment: 404 },
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['removeLabel', 'deleteComment']
    );
    assert.equal(env.core.failed, null);
  });

  it('propagates other errors from deleting the comment', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: filledTemplate(),
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
      failures: { deleteComment: 500 },
    });
    await assert.rejects(run(env), { status: 500 });
  });

  it('posts a new comment when the one it would update is gone', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
      failures: { updateComment: 404 },
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'updateComment', 'createComment']
    );
    assert.equal(env.calls[2].params.issue_number, 7);
    assert.equal(env.calls[2].params.body, env.calls[1].params.body);
    assert.ok(env.core.failed);
  });

  it('propagates other errors from updating the comment', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
      failures: { updateComment: 500 },
    });
    await assert.rejects(run(env), { status: 500 });
  });

  it('passes a pull request with the exempt label', async () => {
    const env = fakeEnvironment({
      title: 'feat: x',
      body: ownFormatBody,
      labels: [EXEMPT_LABEL, NEEDS_TEMPLATE_LABEL],
      comments: [botComment(`${COMMENT_MARKER}\nold text`)],
    });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['removeLabel', 'deleteComment']
    );
    assert.equal(env.core.failed, null);
  });
});
