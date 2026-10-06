// Checks that a pull request description follows .github/pull_request_template.md.
//
// The pr-template-check workflow runs this on pull_request_target from a checkout
// of the base branch tip, so the script and the template come from the base
// branch, not the pull request. The PR title and body are only parsed as text.
//
// The parser approximates how GitHub renders Markdown closely enough for
// descriptions written from the template; it is a compliance check, not the
// record of relicensing consent. The maintainer confirms consent before merging
// (the relicense: consented label, or the agreement on the pull request), so
// formatting crafted to look ticked without rendering as a ticked box is out of
// scope here.
'use strict';

const fs = require('fs');
const path = require('path');

/** Path of the pull request template the description is checked against. */
const TEMPLATE_PATH = path.join(__dirname, '..', 'pull_request_template.md');
const REPO_URL = 'https://github.com/tphakala/birdnet-go';
const TEMPLATE_URL = `${REPO_URL}/blob/main/.github/pull_request_template.md`;
const CONTRIBUTING_URL = `${REPO_URL}/blob/main/CONTRIBUTING.md`;

/** Hidden marker that identifies the check's own comment on a pull request. */
const COMMENT_MARKER = '<!-- pr-template-check -->';
/** Login of the account that posts the comment with the workflow's GITHUB_TOKEN. */
const COMMENT_AUTHOR = 'github-actions[bot]';
/** Label on a pull request whose description does not follow the template. */
const NEEDS_TEMPLATE_LABEL = 'needs: template';
/** Maintainer label that skips the check. */
const EXEMPT_LABEL = 'template: exempt';
/** The maintainer's record that the author has already agreed to the relicensing. */
const CONSENTED_LABEL = 'relicense: consented';
const HTTP_NOT_FOUND = 404;
// The largest page the GitHub REST API returns.
const COMMENTS_PER_PAGE = 100;
// Template section a feature pull request links its agreed issue or discussion in.
const RELATED_SECTION = 'related issue';
// Template sections that hold the required checkboxes.
const CHECKLIST_SECTION = 'checklist';
const LICENSING_SECTION = 'licensing';

// feat: as in Conventional Commits, and feature: as some contributors write it.
const FEATURE_TITLE = /^feat(?:ure)?(\([^)]*\))?!?:/i;
// Linear on purpose: a body can hold a 65k character line, and a pattern with
// several adjacent quantifiers here backtracked for hours on one.
const SECTION_HEADING = /^ {0,3}##[ \t]+(\S[^\n]*)$/;
// A fenced code block opens with three or more backticks or tildes indented at
// most three spaces, and closes on a run of the same character at least as long.
// The info string is matched with [^\n], not ., which stops at U+2028 and made
// a long fence line quadratic.
const CODE_FENCE = /^ {0,3}(`{3,}|~{3,})([^\n]*)$/;
// A list item indented four or more spaces is an indented code block, not a
// checkbox, unless it continues a list; the template's boxes are never nested.
const CHECKBOX = /^ {0,3}[-*+][ \t]+\[([ xX])\][ \t]+([^\n]*)$/;
// A line indented at least two columns, the content offset of a "- [ ]" item at
// the margin, belongs to that item whatever block it starts. An item with a
// deeper offset is over-counted, which fails closed.
const ITEM_CONTENT = /^(?: {2}|\t| \t)/;
// HTML that starts a block (and so ends a paragraph) rather than sitting inline:
// comments, processing instructions, declarations, and common block-level tags.
const HTML_BLOCK_START =
  /^ {0,3}(?:<!--|<\?|<![A-Za-z]|<\/?(?:address|article|aside|blockquote|details|dialog|div|dl|fieldset|figcaption|figure|footer|form|h[1-6]|header|hr|li|main|nav|ol|p|pre|script|section|style|summary|table|tbody|td|textarea|tfoot|th|thead|tr|ul)(?:[\s/>]|$))/i;
// Lines at the margin that start a new block (a list item, heading, block quote
// or thematic break) instead of continuing a box's text.
const BLOCK_START =
  /^ {0,3}(?:(?:[-*+]|\d{1,9}[.)])(?:[ \t]|$)|#{1,6}(?:[ \t]|$)|>|(?:-[ \t]*){3,}$|(?:\*[ \t]*){3,}$|(?:_[ \t]*){3,}$)/;
const COMMENT_OPEN = '<!--';
const COMMENT_CLOSE = '-->';
// "<!-->" and "<!--->" are complete comments, so the closing marker is searched
// for from inside the opening one.
const COMMENT_CLOSE_SEARCH_OFFSET = 2;
// An unclosed comment with at most this many spaces before it on its line starts
// an HTML block that runs to the end of the description.
const BLOCK_MAX_INDENT = 3;
// #123, owner/repo#123, or an issue or discussion URL. A bare #123 must not
// follow a slash, and the owner has no dots, as on GitHub, and must follow a
// boundary that is not part of a path, so URL fragments such as example.com/docs#2
// and example.com/docs/#2 do not count. Each boundary starts one scan bounded by
// its word, so the pattern stays linear on long words.
const ISSUE_REFERENCE =
  /(^|[^\w&/])#\d+\b|(?:^|[^\w./-])[\w-]+\/[\w.-]+#\d+\b|github\.com\/[\w.-]+\/[\w.-]+\/(issues|discussions)\/\d+/i;

// Checkboxes the author must tick. Each is found in the template's section by a
// phrase from its line, and the description must carry that line unchanged.
const CONTRIBUTING_BOX = {
  section: CHECKLIST_SECTION,
  pattern: /contributing guidelines/i,
  label: 'The Contributing Guidelines checkbox',
};
const LICENSING_BOX = {
  section: LICENSING_SECTION,
  pattern: /relicens/i,
  label: 'The relicensing agreement',
  consequence: ' The pull request cannot be merged without it.',
};
const FEATURE_BOX = {
  section: CHECKLIST_SECTION,
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
 * Removes HTML comments roughly the way GitHub hides them: a closed comment is
 * removed, and an unclosed one at the start of a line (after at most three
 * spaces) is removed together with everything after it. An unclosed comment
 * after other text on its line is shown as text by GitHub, so it stays. Comments
 * inside containers (lists, block quotes) and closed comments spanning blocks are
 * approximated; see the scope note at the top of this file.
 * @param {string} markdown
 * @returns {string}
 */
function stripComments(markdown) {
  let out = '';
  let from = 0;
  let closed = true;
  for (;;) {
    const start = markdown.indexOf(COMMENT_OPEN, from);
    if (start < 0) {
      return out + markdown.slice(from);
    }
    // Once one comment has no closing marker, no later one can have it either.
    const end = closed ? markdown.indexOf(COMMENT_CLOSE, start + COMMENT_CLOSE_SEARCH_OFFSET) : -1;
    if (end >= 0) {
      out += markdown.slice(from, start);
      from = end + COMMENT_CLOSE.length;
      continue;
    }
    closed = false;
    if (startsLine(markdown, start)) {
      return out + markdown.slice(from, start);
    }
    out += markdown.slice(from, start + COMMENT_OPEN.length);
    from = start + COMMENT_OPEN.length;
  }
}

/**
 * Reports whether only up to BLOCK_MAX_INDENT spaces precede a position on its
 * line. It looks back a bounded distance, so calling it per comment is linear.
 * @param {string} text
 * @param {number} position
 * @returns {boolean}
 */
function startsLine(text, position) {
  let at = position;
  while (at > 0 && position - at < BLOCK_MAX_INDENT && text[at - 1] === ' ') {
    at--;
  }
  return at === 0 || text[at - 1] === '\n';
}

/**
 * Splits Markdown into its level 2 sections, with HTML comments removed. Task
 * list items outside fenced and indented code are collected per section, each
 * with the text GitHub renders inside its item (continuation lines and indented
 * content included), and a repeated heading adds to its first section.
 * @param {string} markdown
 * @returns {Map<string, {name: string, content: string, boxes: {checked: boolean, text: string}[]}>}
 *   keyed by sectionKey
 */
function parseSections(markdown) {
  const sections = new Map();
  let current = null;
  let openFence = null;
  // The box whose list item is still open, and whether its paragraph is. Like
  // GitHub, indented lines after a box, and unindented lines up to a blank line
  // or a new block, count as part of the box's text.
  let itemBox = null;
  let paragraphOpen = false;
  for (const line of stripComments(markdown).split(/\r?\n/)) {
    // A heading indented into an open item belongs to the item, as on GitHub.
    const heading =
      openFence || (itemBox && ITEM_CONTENT.test(line)) ? null : SECTION_HEADING.exec(line);
    if (heading) {
      const name = stripClosingHashes(heading[1]);
      const key = sectionKey(name);
      // A repeated heading continues the first section rather than replacing it.
      if (!sections.has(key)) {
        sections.set(key, { name, content: '', boxes: [] });
      }
      current = sections.get(key);
      itemBox = null;
      continue;
    }
    if (current) {
      current.content += `${line}\n`;
    }
    if (openFence) {
      const fence = CODE_FENCE.exec(line);
      if (fence && closesFence(openFence, fence)) {
        openFence = null;
      }
      continue;
    }
    const blank = line.trim() === '';
    if (itemBox && !blank && ITEM_CONTENT.test(line)) {
      itemBox.parts.push(normalizeSpace(line));
      continue;
    }
    if (blank) {
      paragraphOpen = false;
      continue;
    }
    const fence = CODE_FENCE.exec(line);
    if (fence && !(fence[1][0] === '`' && fence[2].includes('`'))) {
      openFence = fence[1];
      itemBox = null;
      continue;
    }
    const box = current && CHECKBOX.exec(line);
    if (box) {
      // Text is gathered in parts and joined once, so a long item stays linear.
      itemBox = { checked: box[1] !== ' ', parts: [normalizeSpace(box[2])] };
      paragraphOpen = true;
      current.boxes.push(itemBox);
    } else if (
      itemBox &&
      paragraphOpen &&
      !BLOCK_START.test(line) &&
      !HTML_BLOCK_START.test(line)
    ) {
      itemBox.parts.push(normalizeSpace(line));
    } else {
      itemBox = null;
    }
  }
  for (const section of sections.values()) {
    section.boxes = section.boxes.map(({ checked, parts }) => ({
      checked,
      text: parts.filter(Boolean).join(' '),
    }));
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
 * Trims a line and collapses its runs of whitespace, so extra spacing still
 * compares equal. The lines gathered into one box are joined with a space, so
 * a line re-wrapped onto two lines matches too.
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
    return `${required.label} under **${where}** does not match the template's wording, or has text added inside its list item. Copy it unchanged from the template, tick it, and put any notes outside the list.${consequence}`;
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
  const feature = FEATURE_TITLE.test(title || '');
  for (const [key, { name, boxes }] of templateSections) {
    const section = sections.get(key);
    if (!section) {
      problems.push(`The **${name}** section is missing.`);
    } else if (section.content.trim() === '' && !(feature && key === RELATED_SECTION)) {
      // A section of checkboxes cannot be answered with "does not apply", and an
      // empty Related issue on a feature is reported below as a missing link.
      let fix = 'If it does not apply, say so in a sentence.';
      if (boxes.length === 1) {
        fix = 'Copy its checkbox from the template and tick it.';
      } else if (boxes.length > 1) {
        fix = 'Copy its checkboxes from the template and tick the ones that apply.';
      }
      problems.push(`The **${name}** section is empty. ${fix}`);
    }
  }

  const required = [CONTRIBUTING_BOX, LICENSING_BOX];
  if (feature) {
    required.push(FEATURE_BOX);
    const related = sections.get(RELATED_SECTION);
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
 * Formats problems as a Markdown list, one per line.
 * @param {string[]} problems
 * @returns {string}
 */
function formatList(problems) {
  return problems.map(problem => `- ${problem}`).join('\n');
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
    formatList(problems),
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
 * payload, keeps the label and a single marker comment in sync (removing any
 * duplicates), and fails when the description does not follow the template.
 * @param {{github: object, context: object, core: object}} env the Octokit
 *   client, the workflow run context, and @actions/core, as github-script passes them
 * @returns {Promise<void>}
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
    per_page: COMMENTS_PER_PAGE,
  });
  // Normally there is one marker comment, but a request from a cancelled run can
  // add a second; the first is kept and any others are removed.
  const [comment, ...duplicates] = comments.filter(
    c => c.user && c.user.login === COMMENT_AUTHOR && c.body && c.body.includes(COMMENT_MARKER)
  );
  const deleteComment = ({ id }) =>
    ignoreNotFound(() => github.rest.issues.deleteComment({ owner, repo, comment_id: id }));

  // The label calls below do not trust the payload's label list for the
  // needs-template label: it is a snapshot from when the event fired, and an
  // overlapping run may have changed it since. Label adds and removes are safe
  // to repeat. A request from a cancelled run can still land after this run's;
  // the next event on the pull request corrects the label and the comments.
  if (problems.length === 0) {
    await ignoreNotFound(() =>
      github.rest.issues.removeLabel({ ...issue, name: NEEDS_TEMPLATE_LABEL })
    );
    for (const stale of comment ? [comment, ...duplicates] : []) {
      await deleteComment(stale);
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
  for (const duplicate of duplicates) {
    await deleteComment(duplicate);
  }
  core.setFailed(
    `The pull request description does not follow the template:\n${formatList(problems)}`
  );
}

module.exports = {
  COMMENT_AUTHOR,
  COMMENT_MARKER,
  CONSENTED_LABEL,
  EXEMPT_LABEL,
  NEEDS_TEMPLATE_LABEL,
  TEMPLATE_PATH,
  checkPullRequest,
  parseSections,
  renderComment,
  stripComments,
  run,
};
