// Display helpers for the run ledger (spec cs:agents:runs). Costs are
// integer micro-USD on the wire.

export function formatCost(micros: number): string {
  if (!Number.isFinite(micros) || micros <= 0) {
    return '$0';
  }
  const dollars = micros / 1_000_000;
  if (dollars < 0.01) {
    return '<$0.01';
  }
  if (dollars < 100) {
    return `$${dollars.toFixed(2)}`;
  }
  return `$${Math.round(dollars).toLocaleString('en-US')}`;
}

export function formatTokens(tokens: number): string {
  if (!Number.isFinite(tokens) || tokens <= 0) {
    return '0';
  }
  if (tokens < 1000) {
    return `${Math.round(tokens)}`;
  }
  if (tokens < 1_000_000) {
    return `${(tokens / 1000).toFixed(tokens < 10_000 ? 1 : 0)}k`;
  }
  return `${(tokens / 1_000_000).toFixed(tokens < 10_000_000 ? 1 : 0)}M`;
}

// The server admits only http(s) evidence URLs; this is the second check
// before a string becomes an href. Anything else renders as text.
export function safeEvidenceHref(raw: string): string | null {
  try {
    const url = new URL(raw);
    return url.protocol === 'https:' || url.protocol === 'http:'
      ? url.href
      : null;
  } catch {
    return null;
  }
}
