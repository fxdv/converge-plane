import type { AgentScope } from '@converge/services';

import { Button } from '@converge/ui/components/button';
import { useToast } from '@converge/ui/components/use-toast';
import React from 'react';

import { grantSummary, mcpEndpoint, mcpSnippets } from './agent-access';

// A freshly minted token: the plaintext exists only until this screen closes.
export interface ShownToken {
  token: string;
  agentName: string;
  scopes: AgentScope[] | null;
  teamIds: string[] | null;
  expiresAt?: string;
  external: boolean;
}

export function TokenScreen({
  shown,
  teamName,
  onClose,
}: {
  shown: ShownToken;
  teamName: (id: string) => string;
  onClose: () => void;
}) {
  const { toast } = useToast();
  const grant = grantSummary(
    { tokenScopes: shown.scopes, tokenTeamIds: shown.teamIds },
    teamName,
  );
  const expires = shown.expiresAt
    ? new Date(shown.expiresAt).toLocaleDateString()
    : null;
  const snippets =
    shown.external && typeof window !== 'undefined'
      ? mcpSnippets(mcpEndpoint(window.location.origin))
      : [];

  // navigator.clipboard is undefined outside secure contexts (plain http
  // off localhost), so a failed copy points at manual selection instead.
  const copy = async (text: string, what: string) => {
    try {
      await navigator.clipboard.writeText(text);
      toast({ title: `${what} copied` });
    } catch {
      toast({
        title: `Could not copy the ${what.toLowerCase()}`,
        description: 'Select the text and copy it manually',
      });
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="bg-grayAlpha-100 rounded-md p-3 text-sm font-mono break-all select-all">
        {shown.token}
      </div>
      <div className="text-muted-foreground text-sm">
        Token for &ldquo;{shown.agentName}&rdquo;: {grant.scopes}; {grant.teams}
        {expires && <>; expires {expires}</>}.
      </div>

      {snippets.length > 0 && (
        <div className="flex flex-col gap-2 mt-1">
          <div className="text-sm">Connect a coding agent</div>
          <div className="text-muted-foreground text-sm">
            Put the token in the{' '}
            <span className="font-mono">CONVERGE_TOKEN</span> environment
            variable, then add the server to your client:
          </div>
          {snippets.map((snippet) => (
            <div key={snippet.client} className="flex flex-col gap-1">
              <div className="flex items-center justify-between text-sm">
                <span>
                  {snippet.client}
                  {snippet.where && (
                    <span className="text-muted-foreground font-mono">
                      {' '}
                      {snippet.where}
                    </span>
                  )}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => copy(snippet.text, `${snippet.client} setup`)}
                >
                  Copy
                </Button>
              </div>
              <pre className="bg-grayAlpha-100 rounded-md p-2 text-xs font-mono whitespace-pre-wrap break-all">
                {snippet.text}
              </pre>
            </div>
          ))}
        </div>
      )}

      <div className="flex items-end gap-2 justify-end w-full">
        <Button variant="ghost" onClick={onClose}>
          Done
        </Button>
        <Button variant="secondary" onClick={() => copy(shown.token, 'Token')}>
          Copy token
        </Button>
      </div>
    </div>
  );
}
