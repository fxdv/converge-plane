import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogCancel,
  AlertDialogAction,
  AlertDialogHeader,
  AlertDialogFooter,
} from '@converge/ui/components/alert-dialog';
import { useRouter } from 'next/router';
import React from 'react';

import type { IssueType } from 'common/types';

import { useCurrentTeam } from 'hooks/teams';

import { useDeleteIssueMutation } from 'services/issues';

interface DeleteIssueDialogProps {
  deleteIssueDialog: boolean;
  setDeleteIssueDialog: (value: boolean) => void;
  issue: IssueType;
}

export function DeleteIssueDialog({
  deleteIssueDialog,
  setDeleteIssueDialog,
  issue,
}: DeleteIssueDialogProps) {
  const { mutate: deleteIssue } = useDeleteIssueMutation({});
  const currentTeam = useCurrentTeam();
  const {
    query: { workspaceSlug },
    push,
  } = useRouter();

  const label = currentTeam
    ? `${currentTeam.identifier}-${issue.number}`
    : `issue ${issue.number}`;

  const onDeleteIssue = () => {
    deleteIssue({ issueId: issue.id, teamId: currentTeam.id });
    setDeleteIssueDialog(false);
    push(`/${workspaceSlug}/team/${currentTeam.identifier}/all`);
  };

  return (
    <AlertDialog open={deleteIssueDialog} onOpenChange={setDeleteIssueDialog}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {label}?</AlertDialogTitle>
          <AlertDialogDescription>
            Deleting {label} removes the issue permanently. This cannot be
            undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction onClick={onDeleteIssue}>
            Delete {label}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
