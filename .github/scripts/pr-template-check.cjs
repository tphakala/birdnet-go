// Checks that a pull request description follows .github/pull_request_template.md.
//
// The pr-template-check workflow runs this on pull_request_target from a checkout
// of the base branch tip, so the script and the template come from the base
// branch, not the pull request. The PR title and body are only parsed as text.
'use strict';

const fs = require('fs');
const path = require('path');

const TEMPLATE_PATH = path.join(__dirname, '..', 'pull_request_template.md');
const REPO_URL = 'https://github.com/tphakala/birdnet-go';
const TEMPLATE_URL = `${REPO_URL}/blob/main/.github/pull_request_template.md`;
const CONTRIBUTING_URL = `${REPO_URL}/blob/main/CONTRIBUTING.md`;

const COMMENT_MARKER = '<!-- pr-template-check -->';
const COMMENT_AUTHOR = 'github-actions[bot]';
const NEEDS_TEMPLATE_LABEL = 'needs: template';
const EXEMPT_LABEL = 'template: exempt';
const HTTP_NOT_FOUND = 404;

const FEATURE_TITLE = /^feat(\([^)]*\))?!?:/i;
// Linear on purpose: a body can hold a 65k character line, and a pattern with
// several adjacent quantifiers here backtracked for hours on one.
const SECTION_HEADING = /^##[ \t]+(\S[^\n]*)$/;
const CODE_FENCE = /^\s*(```|~~~)/;
const CHECKBOX = /^\s*[-*+]\s+\[([ xX])\]\s+(.*)$/;
const HTML_COMMENT = /<!--[\s\S]*?-->/g;
const ISSUE_REFERENCE =
  /(^|[^\w&])#\d+\b|github\.com\/[\w.-]+\/[\w.-]+\/(issues|discussions)\/\d+/i;

// Checkboxes the author must tick, found by a phrase from the template line.
const CONTRIBUTING_BOX = /contributing guidelines/i;
const LICENSING_BOX = /relicense/i;
const FEATURE_BOX = /^feature prs only/i;

/**
 * Removes trailing whitespace and the optional closing run of # from an ATX
 * heading's text.
 * @param {string} text
 * @returns {string}
 */
function stripClosingHashes(text) {
  const trimmed = text.trimEnd();
  let end = trimmed.length;
  while (end > 0 && trimmed[end - 1] === '#') {
    end--;
  }
  if (end === trimmed.length || (end > 0 && !/[ \t]/.test(trimmed[end - 1]))) {
    return trimmed;
  }
  return trimmed.slice(0, end).trimEnd();
}

/**
 * Turns a heading into its section key: lower case, without a trailing note in
 * parentheses, so "Licensing" matches "Licensing (required)".
 * @param {string} name
 * @returns {string}
 */
function sectionKey(name) {
  let key = name;
  if (key.endsWith(')')) {
    const open = key.lastIndexOf('(');
    if (open > 0) {
      key = key.slice(0, open).trimEnd();
    }
  }
  return key.toLowerCase();
}

/**
 * Splits Markdown into its level 2 sections, with HTML comments removed.
 * @param {string} markdown
 * @returns {Map<string, {name: string, content: string}>} keyed by sectionKey
 */
function parseSections(markdown) {
  const sections = new Map();
  let current = null;
  let inFence = false;
  for (const line of markdown.replace(HTML_COMMENT, '').split(/\r?\n/)) {
    if (CODE_FENCE.test(line)) {
      inFence = !inFence;
    }
    const heading = inFence ? null : SECTION_HEADING.exec(line);
    if (heading) {
      current = { name: stripClosingHashes(heading[1]), content: '' };
      sections.set(sectionKey(current.name), current);
    } else if (current) {
      current.content += `${line}\n`;
    }
  }
  return sections;
}

/**
 * Lists the task list items in Markdown.
 * @param {string} markdown
 * @returns {{checked: boolean, text: string}[]}
 */
function parseCheckboxes(markdown) {
  return markdown
    .replace(HTML_COMMENT, '')
    .split(/\r?\n/)
    .map(line => CHECKBOX.exec(line))
    .filter(Boolean)
    .map(match => ({ checked: match[1] !== ' ', text: match[2].trim() }));
}

/**
 * Reports a required checkbox that is missing or not ticked.
 * @returns {string|null}
 */
function checkBox(boxes, pattern, missing, unticked) {
  const box = boxes.find(b => pattern.test(b.text));
  if (!box) {
    return missing;
  }
  return box.checked ? null : unticked;
}

/**
 * Compares a pull request with the template.
 * @param {{title: string, body: string|null, template: string}} pr
 * @returns {string[]} problems, empty when the description follows the template
 */
function checkPullRequest({ title, body, template }) {
  const problems = [];
  const text = body || '';
  const sections = parseSections(text);

  for (const [key, { name }] of parseSections(template)) {
    const section = sections.get(key);
    if (!section) {
      problems.push(`The **${name}** section is missing.`);
    } else if (section.content.trim() === '') {
      problems.push(
        `The **${name}** section is empty. If it does not apply, say so in a sentence.`
      );
    }
  }

  const boxes = parseCheckboxes(text);
  const required = [
    [
      CONTRIBUTING_BOX,
      'The Contributing Guidelines checkbox is missing from **Checklist**. Copy it from the template and tick it.',
      'The Contributing Guidelines checkbox under **Checklist** is not ticked.',
    ],
    [
      LICENSING_BOX,
      'The relicensing agreement under **Licensing** is missing. Copy it from the template and tick it. The pull request cannot be merged without it.',
      'The relicensing agreement under **Licensing** is not ticked. The pull request cannot be merged without it.',
    ],
  ];

  if (FEATURE_TITLE.test(title || '')) {
    required.push([
      FEATURE_BOX,
      'This is a feature pull request, but the "Feature PRs only" checkbox is missing from **Checklist**. Copy it from the template and tick it.',
      'This is a feature pull request, but the "Feature PRs only" checkbox under **Checklist** is not ticked.',
    ]);
    const related = sections.get('related issue');
    if (related && !ISSUE_REFERENCE.test(related.content)) {
      problems.push(
        'Feature pull requests must link the issue or discussion where the feature was agreed with the maintainer, under **Related issue**.'
      );
    }
  }

  for (const [pattern, missing, unticked] of required) {
    const problem = checkBox(boxes, pattern, missing, unticked);
    if (problem) {
      problems.push(problem);
    }
  }
  return problems;
}

/**
 * Builds the comment that lists what the description is missing.
 * @param {string[]} problems
 * @returns {string}
 */
function renderComment(problems) {
  return [
    COMMENT_MARKER,
    `Thanks for the pull request. Before it is reviewed, the description needs to follow the [pull request template](${TEMPLATE_URL}):`,
    '',
    ...problems.map(problem => `- ${problem}`),
    '',
    `Edit the description to fix these; this check runs again on every edit. If a tool wrote its own description, replace it with the template's text and fill that in. See [Pull Request Process](${CONTRIBUTING_URL}#pull-request-process), and for features also [Fixes and Features](${CONTRIBUTING_URL}#fixes-and-features-what-to-expect) and [Feature Ownership](${CONTRIBUTING_URL}#feature-ownership).`,
  ].join('\n');
}

/**
 * Removes a label, ignoring one that is already gone.
 */
async function removeLabel(github, params) {
  try {
    await github.rest.issues.removeLabel(params);
  } catch (error) {
    if (error.status !== HTTP_NOT_FOUND) {
      throw error;
    }
  }
}

/**
 * Entry point for actions/github-script: checks the pull request in the event
 * payload, keeps the label and the comment in sync, and fails when the
 * description does not follow the template.
 */
async function run({ github, context, core }) {
  const pr = context.payload.pull_request;
  const { owner, repo } = context.repo;
  const issue = { owner, repo, issue_number: pr.number };
  const labels = pr.labels.map(label => label.name);

  let problems = [];
  if (labels.includes(EXEMPT_LABEL)) {
    core.info(`Skipping the check: the pull request has the "${EXEMPT_LABEL}" label.`);
  } else {
    const template = fs.readFileSync(TEMPLATE_PATH, 'utf8');
    problems = checkPullRequest({ title: pr.title, body: pr.body, template });
  }

  const comments = await github.paginate(github.rest.issues.listComments, {
    ...issue,
    per_page: 100,
  });
  const comment = comments.find(
    c => c.user && c.user.login === COMMENT_AUTHOR && c.body.includes(COMMENT_MARKER)
  );

  if (problems.length === 0) {
    if (labels.includes(NEEDS_TEMPLATE_LABEL)) {
      await removeLabel(github, { ...issue, name: NEEDS_TEMPLATE_LABEL });
    }
    if (comment) {
      await github.rest.issues.deleteComment({ owner, repo, comment_id: comment.id });
    }
    core.info('The pull request description follows the template.');
    return;
  }

  if (!labels.includes(NEEDS_TEMPLATE_LABEL)) {
    await github.rest.issues.addLabels({ ...issue, labels: [NEEDS_TEMPLATE_LABEL] });
  }
  const body = renderComment(problems);
  if (!comment) {
    await github.rest.issues.createComment({ ...issue, body });
  } else if (comment.body !== body) {
    await github.rest.issues.updateComment({ owner, repo, comment_id: comment.id, body });
  }
  core.setFailed(
    `The pull request description does not follow the template:\n${problems.map(p => `- ${p}`).join('\n')}`
  );
}

module.exports = {
  COMMENT_MARKER,
  EXEMPT_LABEL,
  NEEDS_TEMPLATE_LABEL,
  TEMPLATE_PATH,
  checkPullRequest,
  renderComment,
  run,
};
