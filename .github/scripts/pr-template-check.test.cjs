'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const { describe, it } = require('node:test');

const {
  COMMENT_MARKER,
  EXEMPT_LABEL,
  NEEDS_TEMPLATE_LABEL,
  TEMPLATE_PATH,
  checkPullRequest,
  renderComment,
  run,
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

  it('rejects an empty body', () => {
    const problems = checkPullRequest({ title: 'fix: something', body: null, template });
    assert.ok(problems.length > 0);
    assert.ok(problems.some(p => p.startsWith('The relicensing agreement')));
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
    assert.ok(Date.now() - started < 500, `took ${Date.now() - started} ms`);
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
    const missing = (heading, body) =>
      checkPullRequest({ title: 'fix: x', body, template: `${heading}\n` }).filter(p =>
        p.includes(' section ')
      );
    // A tab after ##, a tab before the closing run, a stray carriage return.
    assert.deepEqual(missing('## Notes', '##\tNotes\ntext'), []);
    assert.deepEqual(missing('## Notes', '## Notes\t##  \ntext'), []);
    assert.deepEqual(missing('## Notes', '## Notes\r\r\ntext'), []);
    // A heading that is only a closing run has empty text, not "##".
    assert.deepEqual(missing('## Notes', '## ##\ntext'), ['The **Notes** section is missing.']);
    // Only a trailing note after other text is dropped from the key.
    assert.deepEqual(missing('## (note)', '## (note)\ntext'), []);
    assert.deepEqual(missing('## Notes', '## Notes)\ntext'), ['The **Notes** section is missing.']);
    assert.deepEqual(missing('## Notes (a)', '## Notes\ntext'), []);
  });

  it('matches a heading without the note in parentheses', () => {
    const body = filledTemplate({ tick: untickFeatureBox }).replace(
      '## Licensing (required)',
      '## Licensing'
    );
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });

  it('ignores headings inside code blocks', () => {
    const body = `${filledTemplate({ tick: untickFeatureBox })}\n\`\`\`\n## Licensing\n\`\`\`\n`;
    assert.deepEqual(checkPullRequest({ title: 'fix: something', body, template }), []);
  });
});

/** A fake of the parts of the github-script environment that run() uses. */
function fakeEnvironment({ title, body, labels = [], comments = [] }) {
  const calls = [];
  const record = name => async params => {
    calls.push({ name, params });
    return {};
  };
  const github = {
    paginate: async () => comments,
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

const botComment = body => ({ id: 99, user: { login: 'github-actions[bot]' }, body });

describe('run', () => {
  it('labels, comments and fails when the description does not follow the template', async () => {
    const env = fakeEnvironment({ title: 'feat: x', body: ownFormatBody });
    await run(env);
    assert.deepEqual(
      env.calls.map(c => c.name),
      ['addLabels', 'createComment']
    );
    assert.deepEqual(env.calls[0].params.labels, [NEEDS_TEMPLATE_LABEL]);
    assert.ok(env.calls[1].params.body.startsWith(COMMENT_MARKER));
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
      ['updateComment']
    );
    assert.equal(env.calls[0].params.comment_id, 99);
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
    assert.deepEqual(env.calls, []);
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
      ['createComment']
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
    assert.equal(env.core.failed, null);
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
