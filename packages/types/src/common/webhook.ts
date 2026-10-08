import { IntegrationAccount } from '../integration-account';

export type EventBody = Record<string, any>;

export type EventHeaders = Record<string, any>;

export type EventQueryParams = Record<string, any>;

export interface WebhookPayload {
  eventBody: EventBody;
  eventHeaders: EventHeaders;
  integrationAccounts: Record<string, IntegrationAccount>;
  userId: string;
}
