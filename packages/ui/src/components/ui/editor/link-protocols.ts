// Link hrefs the editor will parse, render or create. Anything carrying a
// scheme must be one of these; scheme-less hrefs (relative paths, #anchors,
// //host) stay allowed. Kept dependency-free so web/scripts/editor-smoke.mjs
// can execute it.
export const ALLOWED_LINK_PROTOCOLS = ['http', 'https', 'mailto'] as const;

const SCHEME = /^([a-z][a-z0-9+.-]*):/;

export function isAllowedLinkUri(uri: string | undefined | null): boolean {
  if (!uri) {
    return false;
  }
  // Browsers ignore ASCII whitespace/control characters inside a scheme
  // ("java\nscript:"), so the check must too.
  // eslint-disable-next-line no-control-regex
  const normalized = uri.replace(/[\u0000- \u007f-\u009f]/g, '');
  const match = SCHEME.exec(normalized.toLowerCase());
  if (!match) {
    return true;
  }
  return (ALLOWED_LINK_PROTOCOLS as readonly string[]).includes(match[1]);
}
