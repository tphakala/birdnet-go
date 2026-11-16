// One alternation so the leftmost comment wins: a /* inside a line comment, or a // inside a block
// comment, stays part of that comment. Block and line comments must start a line or follow
// whitespace (a line comment may also follow ;), so a URL, a protocol relative href or a glob in
// a string is not taken for one.
const COMMENT = /<!--[\s\S]*?-->|(?<=^|\s)\/\*[\s\S]*?\*\/|(?<=^|[\s;])\/\/.*$/gm;

/** Removes HTML, block and line comments, keeping every newline so line numbers stay true. */
export function stripComments(source: string): string {
  return source.replace(COMMENT, comment => comment.replace(/[^\n]/g, ''));
}
