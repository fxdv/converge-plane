// An imported GitHub issue stores its description as a JSON string,
// "Imported from <url>" plus the body. The rich-text editor would
// treat that string as HTML. This splits the source link from the body.

const IMPORTED =
  /^Imported from (https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+\/issues\/[1-9]\d*)\s*/;

export function importedIssueText(
  raw: string | null | undefined,
): { href: string; body: string } | null {
  if (!raw) {
    return null;
  }
  let text = raw;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (typeof parsed !== 'string') {
      return null;
    }
    text = parsed;
  } catch {
    // A value that is already plain text.
  }
  const match = text.match(IMPORTED);
  if (!match) {
    return null;
  }
  return {
    href: match[1],
    body: text.slice(match[0].length).replace(/^\n+/, ''),
  };
}
