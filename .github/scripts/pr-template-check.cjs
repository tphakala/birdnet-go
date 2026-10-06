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
// The maintainer's record that the author has already agreed to the relicensing.
const CONSENTED_LABEL = 'relicense: consented';
const HTTP_NOT_FOUND = 404;

const FEATURE_TITLE = /^feat(\([^)]*\))?!?:/i;
// Linear on purpose: a body can hold a 65k character line, and a pattern with
// several adjacent quantifiers here backtracked for hours on one.
const SECTION_HEADING = /^##[ \t]+(\S[^\n]*)$/;
// A fenced code block opens with three or more backticks or tildes indented at
// most three spaces, and closes on a run of the same character at least as long.
const CODE_FENCE = /^ {0,3}(`{3,}|~{3,})(.*)$/;
// A list item indented four or more spaces is an indented code block, not a
// checkbox, unless it continues a list; the template's boxes are never nested.
const CHECKBOX = /^ {0,3}[-*+][ \t]+\[([ xX])\][ \t]+(.*)$/;
// An unclosed comment hides the rest of the description when GitHub renders it.
const HTML_COMMENT = /<!--[\s\S]*?(?:-->|$)/g;
// #123, owner/repo#123, or an issue or discussion URL. The owner/repo form is
// matched from its slash so the pattern stays linear on long words.
const ISSUE_REFERENCE =
  /(^|[^\w&])#\d+\b|\/[\w.-]+#\d+\b|github\.com\/[\w.-]+\/[\w.-]+\/(issues|discussions)\/\d+/i;

// Checkboxes the author must tick. Each is found in the template's section by a
// phrase from its line, and the description must carry that line unchanged.
const CONTRIBUTING_BOX = {
  section: 'checklist',
  pattern: /contributing guidelines/i,
  label: 'The Contributing Guidelines checkbox',
};
const LICENSING_BOX = {
  section: 'licensing',
  pattern: /relicense/i,
  label: 'The relicensing agreement',
  consequence: ' The pull request cannot be merged without it.',
};
const FEATURE_BOX = {
  section: 'checklist',
  pattern: /^feature prs only/i,
  label: 'This is a feature pull request, but the "Feature PRs only" checkbox',
};

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
 * Drops a trailing note in parentheses from a heading, so "Licensing
 * (required)" reads as "Licensing".
 * @param {string} name
 * @returns {string}
 */
function withoutNote(name) {
  if (name.endsWith(')')) {
    const open = name.lastIndexOf('(');
    if (open > 0) {
      return name.slice(0, open).trimEnd();
    }
  }
  return name;
}

/**
 * Turns a heading into its section key: lower case and without a trailing
 * note, so "Licensing" matches "Licensing (required)".
 * @param {string} name
 * @returns {string}
 */
function sectionKey(name) {
  return withoutNote(name).toLowerCase();
}

/**
 * Splits Markdown into its level 2 sections, with HTML comments removed. Task
 * list items outside fenced and indented code are collected per section, and a
 * repeated heading adds to its first section.
 * @param {string} markdown
 * @returns {Map<string, {name: string, content: string, boxes: {checked: boolean, text: string}[]}>}
 *   keyed by sectionKey
 */
function parseSections(markdown) {
  const sections = new Map();
  let current = null;
  let openFence = null;
  for (const line of markdown.replace(HTML_COMMENT, '').split(/\r?\n/)) {
    const fence = CODE_FENCE.exec(line);
    if (openFence) {
      if (fence && closesFence(openFence, fence)) {
        openFence = null;
      }
      if (current) {
        current.content += `${line}\n`;
      }
      continue;
    }
    if (fence && !(fence[1][0] === '`' && fence[2].includes('`'))) {
      openFence = fence[1];
    }
    const heading = openFence ? null : SECTION_HEADING.exec(line);
    if (heading) {
      const name = stripClosingHashes(heading[1]);
      const key = sectionKey(name);
      // A repeated heading continues the first section rather than replacing it.
      if (!sections.has(key)) {
        sections.set(key, { name, content: '', boxes: [] });
      }
      current = sections.get(key);
    } else if (current) {
      current.content += `${line}\n`;
      const box = openFence ? null : CHECKBOX.exec(line);
      if (box) {
        current.boxes.push({ checked: box[1] !== ' ', text: normalizeSpace(box[2]) });
      }
    }
  }
  return sections;
}

/**
 * Reports whether a fence line closes the open fence: the same character, at
 * least as long, and nothing after it.
 * @param {string} openFence the opening run of backticks or tildes
 * @param {RegExpExecArray} fence a CODE_FENCE match
 * @returns {boolean}
 */
function closesFence(openFence, fence) {
  return (
    fence[1][0] === openFence[0] && fence[1].length >= openFence.length && fence[2].trim() === ''
  );
}

/**
 * Trims a line and collapses its runs of spaces and tabs, so extra spacing
 * still compares equal. A line re-wrapped onto two lines does not.
 * @param {string} text
 * @returns {string}
 */
function normalizeSpace(text) {
  return text.trim().split(/\s+/).join(' ');
}

/**
 * Reports a required checkbox that is missing, reworded or not ticked.
 * @param {Map} sections the description's sections
 * @param {Map} templateSections the template's sections
 * @param {{section: string, pattern: RegExp, label: string, consequence?: string}} required
 * @returns {string|null}
 */
function checkBox(sections, templateSections, required) {
  const templateSection = templateSections.get(required.section);
  const expected =
    templateSection && templateSection.boxes.find(b => required.pattern.test(b.text));
  if (!expected) {
    throw new Error(
      `The PR template has no checkbox matching ${required.pattern} under "${required.section}"; update pr-template-check.cjs.`
    );
  }
  const where = withoutNote(templateSection.name);
  const consequence = required.consequence || '';
  const boxes = sections.has(required.section) ? sections.get(required.section).boxes : [];
  // Every copy of the line must be ticked, so one ticked copy cannot outvote
  // an unticked one.
  const copies = boxes.filter(b => b.text === expected.text);
  if (copies.length > 0) {
    return copies.every(b => b.checked)
      ? null
      : `${required.label} under **${where}** is not ticked.${consequence}`;
  }
  if (boxes.some(b => required.pattern.test(b.text))) {
    return `${required.label} under **${where}** does not match the template's wording. Copy it unchanged from the template and tick it.${consequence}`;
  }
  return `${required.label} is missing from **${where}**. Copy it from the template and tick it.${consequence}`;
}

/**
 * Compares a pull request with the template.
 * @param {{title: string, body: string|null, template: string, labels?: string[]}} pr
 *   labels: a relicense: consented label stands in for the relicensing box
 * @returns {string[]} problems, empty when the description follows the template
 * @throws {Error} when the template itself lacks a required checkbox
 */
function checkPullRequest({ title, body, template, labels = [] }) {
  const problems = [];
  const text = body || '';
  const sections = parseSections(text);

  const templateSections = parseSections(template);
  for (const [key, { name }] of templateSections) {
    const section = sections.get(key);
    if (!section) {
      problems.push(`The **${name}** section is missing.`);
    } else if (section.content.trim() === '') {
      problems.push(
        `The **${name}** section is empty. If it does not apply, say so in a sentence.`
      );
    }
  }

  const required = [CONTRIBUTING_BOX, LICENSING_BOX];
  if (FEATURE_TITLE.test(title || '')) {
    required.push(FEATURE_BOX);
    const related = sections.get('related issue');
    if (related && !ISSUE_REFERENCE.test(related.content)) {
      problems.push(
        'Feature pull requests must link the issue or discussion where the feature was agreed with the maintainer, under **Related issue**.'
      );
    }
  }

  const consented = labels.includes(CONSENTED_LABEL);
  for (const box of required) {
    // Checked even when consent is recorded, so a template that loses the box
    // still fails loudly.
    const problem = checkBox(sections, templateSections, box);
    if (problem && !(box === LICENSING_BOX && consented)) {
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
 * Runs a GitHub API call, treating 404 Not Found as the target being gone.
 * @param {() => Promise<unknown>} call
 * @returns {Promise<boolean>} false when the call returned 404
 */
async function ignoreNotFound(call) {
  try {
    await call();
    return true;
  } catch (error) {
    if (error.status !== HTTP_NOT_FOUND) {
      throw error;
    }
    return false;
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
    problems = checkPullRequest({ title: pr.title, body: pr.body, template, labels });
  }

  const comments = await github.paginate(github.rest.issues.listComments, {
    ...issue,
    per_page: 100,
  });
  const comment = comments.find(
    c => c.user && c.user.login === COMMENT_AUTHOR && c.body.includes(COMMENT_MARKER)
  );

  // The label calls below do not trust the payload's label list for the
  // needs-template label: it is a snapshot from when the event fired, and an
  // overlapping run may have changed it since. Label adds and removes are safe
  // to repeat. A request from a cancelled run can still land after this run's;
  // the next event on the pull request corrects the label and the comment.
  if (problems.length === 0) {
    await ignoreNotFound(() =>
      github.rest.issues.removeLabel({ ...issue, name: NEEDS_TEMPLATE_LABEL })
    );
    if (comment) {
      await ignoreNotFound(() =>
        github.rest.issues.deleteComment({ owner, repo, comment_id: comment.id })
      );
    }
    core.info('The pull request description follows the template.');
    return;
  }

  await github.rest.issues.addLabels({ ...issue, labels: [NEEDS_TEMPLATE_LABEL] });
  const body = renderComment(problems);
  if (!comment) {
    await github.rest.issues.createComment({ ...issue, body });
  } else if (comment.body !== body) {
    const updated = await ignoreNotFound(() =>
      github.rest.issues.updateComment({ owner, repo, comment_id: comment.id, body })
    );
    if (!updated) {
      await github.rest.issues.createComment({ ...issue, body });
    }
  }
  core.setFailed(
    `The pull request description does not follow the template:\n${problems.map(p => `- ${p}`).join('\n')}`
  );
}

module.exports = {
  COMMENT_MARKER,
  CONSENTED_LABEL,
  EXEMPT_LABEL,
  NEEDS_TEMPLATE_LABEL,
  TEMPLATE_PATH,
  checkPullRequest,
  parseSections,
  renderComment,
  run,
};
