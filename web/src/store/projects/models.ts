import { types } from 'mobx-state-tree';

// The client model for the Project sync record. The shape is the wire
// contract (spec cs:api:projects): the server's projectData mirrors this
// interface, status is the v1 constant ACTIVE, and color/dates/lead are
// nullable on the wire (a project without them reads as null, never
// missing — the union admits both null and undefined for retained
// payloads).
export const Project = types.model({
  id: types.string,
  createdAt: types.string,
  updatedAt: types.string,
  name: types.string,
  status: types.string,
  teams: types.array(types.string),
  workspaceId: types.string,

  color: types.union(types.null, types.string),
  description: types.union(types.null, types.string),
  startDate: types.union(types.null, types.string),
  endDate: types.union(types.null, types.string),
  leadUserId: types.union(types.null, types.string),
});
