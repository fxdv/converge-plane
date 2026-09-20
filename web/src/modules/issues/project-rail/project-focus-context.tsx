import React from 'react';

// The board's project-focus lens (spec cs:ui:projects-rail): which
// project the reader has focused, and its color, so the cards can ring
// (match) or dim (everything else).
//
// The board owns the state; the card reads it. `null` is the no-lens
// default, and it matters: BoardIssueItem is shared with every other
// board view (label / assignee / team / priority, none of which has a
// project rail), and those must render byte-identical to a board with
// no lens active.
export interface ProjectFocus {
  projectId: string;
  color: string;
}

export const ProjectFocusContext = React.createContext<ProjectFocus | null>(
  null,
);
