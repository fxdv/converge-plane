import { CreateNewTeam } from './create-new-team';
import { Export } from './export';
import { Integrations } from './integrations';
import { Labels } from './labels';
import { Members } from './members';
import { Overview } from './overview';

export const SECTION_COMPONENTS = {
  overview: Overview,
  labels: Labels,
  members: Members,
  new_team: CreateNewTeam,
  export: Export,
  integrations: Integrations,
};

export const SECTION_TITLES = {
  overview: 'Overview',
  labels: 'Labels',
  members: 'Members',
  new_team: 'Add team',
  export: 'Export',
  integrations: 'Integrations',
};

type StringKeys<T> = {
  [K in keyof T]: T[K] extends string ? K : never;
}[keyof T];

export type SECTION_COMPONENTS_KEYS = StringKeys<typeof SECTION_COMPONENTS>;
