import { Button } from '@converge/ui/components/button';
import React from 'react';
import { useQuery, useQueryClient } from 'react-query';

import { SettingSection } from 'modules/settings/setting-section';

import { useCurrentWorkspace } from 'hooks/workspace';

import { ajaxGet, ajaxPost } from 'services/utils';

import { UserContext } from 'store/user-context';

interface WebhookEndpoint {
  id: string;
  url: string;
  enabled: boolean;
  createdAt: string;
}

interface WebhookDelivery {
  id: string;
  event: string;
  attempts: number;
  lastError: string | null;
  deliveredAt: string | null;
  nextAttemptAt: string;
  createdAt: string;
}

export function Integrations() {
  const workspace = useCurrentWorkspace();
  const user = React.useContext(UserContext);
  const queryClient = useQueryClient();
  const repos = user?.features?.githubRepos ?? [];
  const tracking = user?.features?.githubPullRequests === true;

  const endpoints = useQuery(
    ['webhooks', workspace?.id],
    () =>
      ajaxGet<WebhookEndpoint[]>({
        url: `/api/v1/workspaces/${workspace.id}/webhooks`,
      }),
    { enabled: !!workspace?.id, retry: false },
  );
  const deliveries = useQuery(
    ['webhook-deliveries', workspace?.id],
    () =>
      ajaxGet<WebhookDelivery[]>({
        url: `/api/v1/workspaces/${workspace.id}/webhooks/deliveries`,
      }),
    { enabled: !!workspace?.id, retry: false },
  );

  const retry = async (eventId: string) => {
    await ajaxPost({
      url: `/api/v1/workspaces/${workspace.id}/webhooks/deliveries/${eventId}/retry`,
    });
    await queryClient.invalidateQueries(['webhook-deliveries', workspace?.id]);
  };

  return (
    <div className="flex flex-col gap-8">
      <SettingSection
        title="GitHub"
        description="Repositories the server reads. Issue copies and pull request checks use this list. The token stays on the server."
      >
        {tracking && repos.length > 0 ? (
          <ul className="text-sm font-mono">
            {repos.map((repo) => (
              <li key={repo}>{repo}</li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">
            GitHub tracking is off. Set CONVERGE_GITHUB_REPOS on the server.
          </p>
        )}
      </SettingSection>
      <SettingSection
        title="Webhook deliveries"
        description="Recent outbound events for this workspace, and a way to send one again."
      >
        {endpoints.isError || deliveries.isError ? (
          <p className="text-sm text-muted-foreground">
            Owners and admins can see deliveries.
          </p>
        ) : (
          <div className="flex flex-col gap-4 text-sm">
            <div>
              {(endpoints.data ?? []).length === 0
                ? 'No endpoints yet.'
                : (endpoints.data ?? []).map((endpoint) => (
                    <div key={endpoint.id} className="truncate">
                      {endpoint.url}
                    </div>
                  ))}
            </div>
            <div className="flex flex-col gap-2">
              {(deliveries.data ?? []).length === 0
                ? 'No deliveries yet.'
                : (deliveries.data ?? []).map((delivery) => (
                    <div
                      key={delivery.id}
                      className="flex items-center gap-3 border-b border-border py-2"
                    >
                      <span className="font-mono">{delivery.event}</span>
                      <span className="text-muted-foreground truncate">
                        {delivery.deliveredAt
                          ? 'delivered'
                          : (delivery.lastError ?? 'waiting')}
                      </span>
                      <span className="flex-1" />
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => {
                          void retry(delivery.id);
                        }}
                      >
                        Retry
                      </Button>
                    </div>
                  ))}
            </div>
          </div>
        )}
      </SettingSection>
    </div>
  );
}
