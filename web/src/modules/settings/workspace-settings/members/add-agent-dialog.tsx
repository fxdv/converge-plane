import { zodResolver } from '@hookform/resolvers/zod';
import { Button } from '@converge/ui/components/button';
import {
  DialogContent,
  Dialog,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@converge/ui/components/form';
import { Input } from '@converge/ui/components/input';
import { MultiSelect } from '@converge/ui/components/multi-select';
import { useToast } from '@converge/ui/components/use-toast';
import React from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import type { TeamType } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace/use-current-workspace';

import { useCreateAgentMutation } from 'services/workspace';

import { useContextStore } from 'store/global-context-provider';

interface AddAgentDialogProps {
  setDialogOpen: (value: boolean) => void;
}

const AddAgentDialogSchema = z.object({
  name: z.string().min(1, { message: 'A name is required' }).max(64),
  teamIds: z
    .array(z.string())
    .min(1, { message: 'At least one team should be selected' }),
});

type AddAgentForm = z.infer<typeof AddAgentDialogSchema>;

export function AddAgentDialog({ setDialogOpen }: AddAgentDialogProps) {
  const { toast } = useToast();
  const { teamsStore } = useContextStore();
  const workspace = useCurrentWorkspace();

  const form = useForm<AddAgentForm>({
    resolver: zodResolver(AddAgentDialogSchema),
    defaultValues: { name: '', teamIds: [] },
  });

  // The token is returned exactly once; the dialog switches to a copy
  // screen until the user explicitly closes it.
  const [issuedToken, setIssuedToken] = React.useState<string | null>(null);
  const [agentName, setAgentName] = React.useState('');

  const onClose = () => {
    setDialogOpen(false);
  };

  const { mutate: createAgent, isLoading } = useCreateAgentMutation({
    onSuccess: (data) => {
      if (!data.token) {
        toast({
          title: 'Agent created',
          description: 'No token was returned',
        });
        onClose();
        return;
      }
      setAgentName(data.name);
      setIssuedToken(data.token);
      toast({
        title: `Agent "${data.name}" created`,
        description: 'Copy the token before closing',
      });
    },
    onError: (message) => {
      toast({
        title: 'Could not create agent',
        description: message,
      });
    },
  });

  const onSubmit = (values: AddAgentForm) => {
    if (!workspace?.id) {
      toast({
        title: 'No workspace selected',
      });
      return;
    }
    createAgent({
      workspaceId: workspace.id,
      name: values.name,
      teamIds: values.teamIds,
    });
  };

  const copyToken = async () => {
    if (issuedToken) {
      await navigator.clipboard.writeText(issuedToken);
      toast({
        title: 'Token copied',
        description: 'It will not be shown again',
      });
    }
  };

  return (
    <Dialog open onOpenChange={setDialogOpen}>
      <DialogContent className="sm:max-w-[600px] p-6">
        <DialogHeader className="pb-0">
          <DialogTitle className="font-normal flex flex-col gap-1">
            <div className="flex gap-1 items-center">
              {issuedToken ? 'Agent token' : 'Add agent'}
            </div>
            <div className="text-muted-foreground text-left text-base leading-5 max-w-[360px]">
              {issuedToken
                ? 'This token is the agent only credential. It will not be shown again.'
                : 'Agents work issues through an API token instead of signing in'}
            </div>
          </DialogTitle>
        </DialogHeader>

        {issuedToken ? (
          <div className="flex flex-col gap-3">
            <div className="bg-grayAlpha-100 rounded-md p-3 text-sm font-mono break-all select-all">
              {issuedToken}
            </div>
            <div className="text-muted-foreground text-sm">
              {agentName ? `Save it for the "${agentName}" runner` : 'Save it for the agent runner'},
              {' '}or rotate a fresh one later from the members list.
            </div>
            <div className="flex items-end gap-2 justify-end w-full">
              <Button variant="ghost" onClick={onClose}>
                Done
              </Button>
              <Button variant="secondary" onClick={copyToken}>
                Copy token
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex flex-col gap-2 items-center w-full">
            <Form {...form}>
              <form
                onSubmit={form.handleSubmit(onSubmit)}
                className="w-full flex flex-col gap-2"
              >
                <FormField
                  control={form.control}
                  name="name"
                  render={({ field }) => (
                    <FormItem className="w-full">
                      <FormLabel>Name</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          className="focus-visible:ring-0 bg-grayAlpha-100 w-full"
                          placeholder="scout"
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="teamIds"
                  render={({ field }) => (
                    <FormItem className="my-3">
                      <FormLabel>Add to teams </FormLabel>

                      <FormControl>
                        <MultiSelect
                          placeholder="Select teams"
                          options={teamsStore.teams.map(
                            (team: TeamType) => ({
                              value: team.id,
                              label: team.name,
                            }),
                          )}
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className="flex items-end gap-2 justify-end w-full mt-2">
                  <Button variant="ghost" type="button" onClick={onClose}>
                    Cancel
                  </Button>
                  <Button variant="secondary" type="submit" isLoading={isLoading}>
                    Create agent
                  </Button>
                </div>
              </form>
            </Form>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
