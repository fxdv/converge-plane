import type { AgentData, AgentDriver } from '@converge/services';

import { Button } from '@converge/ui/components/button';
import { Checkbox } from '@converge/ui/components/checkbox';
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@converge/ui/components/select';
import { useToast } from '@converge/ui/components/use-toast';
import { zodResolver } from '@hookform/resolvers/zod';
import React from 'react';
import { useForm } from 'react-hook-form';
import { z } from 'zod';

import type { TeamType } from 'common/types';

import { useCurrentWorkspace } from 'hooks/workspace/use-current-workspace';

import { useCreateAgentMutation } from 'services/workspace';

import { useContextStore } from 'store/global-context-provider';

import {
  AGENT_SCOPES,
  DRIVER_HINT,
  EXPIRY_OPTIONS,
  accessProblem,
  agentDefaults,
  agentRequest,
  type AgentAccessValues,
} from './agent-access';
import { TokenScreen } from './token-screen';

interface AddAgentDialogProps {
  setDialogOpen: (value: boolean) => void;
}

const AddAgentDialogSchema = z
  .object({
    name: z.string().min(1, { message: 'A name is required' }).max(64),
    teamIds: z
      .array(z.string())
      .min(1, { message: 'At least one team should be selected' }),
    driver: z.enum(['runtime', 'external']),
    access: z.enum(['scoped', 'full']),
    scopes: z.array(z.string()),
    teamLimited: z.boolean(),
    expiryHours: z.number(),
  })
  .superRefine((values, ctx) => {
    const problem = accessProblem(values as AgentAccessValues);
    if (problem) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['scopes'],
        message: problem,
      });
    }
  });

type AddAgentForm = z.infer<typeof AddAgentDialogSchema>;

export function AddAgentDialog({ setDialogOpen }: AddAgentDialogProps) {
  const { toast } = useToast();
  const { teamsStore } = useContextStore();
  const workspace = useCurrentWorkspace();

  const form = useForm<AddAgentForm>({
    resolver: zodResolver(AddAgentDialogSchema),
    defaultValues: {
      name: '',
      teamIds: [],
      driver: 'runtime',
      ...agentDefaults('runtime'),
    },
  });
  const driver = form.watch('driver');
  const access = form.watch('access');

  // The token is returned exactly once; the dialog switches to a copy
  // screen until the user explicitly closes it.
  const [created, setCreated] = React.useState<AgentData | null>(null);

  const onClose = () => {
    setDialogOpen(false);
  };

  const { mutate: createAgent, isPending } = useCreateAgentMutation({
    onSuccess: (data) => {
      if (!data.token) {
        toast({
          title: 'Agent created',
          description: 'No token was returned',
        });
        onClose();
        return;
      }
      setCreated(data);
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
      ...agentRequest(values as AgentAccessValues),
    });
  };

  const onDriverChange = (value: AgentDriver) => {
    form.setValue('driver', value);
    const defaults = agentDefaults(value);
    form.setValue('access', defaults.access);
    form.setValue('scopes', defaults.scopes);
    form.setValue('teamLimited', defaults.teamLimited);
    form.setValue('expiryHours', defaults.expiryHours);
    form.clearErrors('scopes');
  };

  const teamName = (id: string) =>
    teamsStore.teams.find((team: TeamType) => team.id === id)?.name ?? id;

  return (
    <Dialog open onOpenChange={setDialogOpen}>
      <DialogContent className="sm:max-w-[600px] p-6 max-h-[90vh] overflow-y-auto">
        <DialogHeader className="pb-0">
          <DialogTitle className="font-normal flex flex-col gap-1">
            <div className="flex gap-1 items-center">
              {created ? 'Agent token' : 'Add agent'}
            </div>
            <div className="text-muted-foreground text-left text-base leading-5 max-w-[420px]">
              {created
                ? "This token is the agent's only credential. It will not be shown again."
                : 'Agents work issues through an API token instead of signing in'}
            </div>
          </DialogTitle>
        </DialogHeader>

        {created?.token ? (
          <TokenScreen
            shown={{
              token: created.token,
              agentName: created.name,
              scopes: created.tokenScopes,
              teamIds: created.tokenTeamIds,
              expiresAt: created.tokenExpiresAt,
              external: created.driver === 'external',
            }}
            teamName={teamName}
            onClose={onClose}
          />
        ) : (
          <div className="flex flex-col gap-2 items-center w-full">
            <Form {...form}>
              <form
                onSubmit={form.handleSubmit(onSubmit)}
                className="w-full flex flex-col gap-3"
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
                    <FormItem>
                      <FormLabel>Add to teams</FormLabel>
                      <FormControl>
                        <MultiSelect
                          placeholder="Select teams"
                          options={teamsStore.teams.map((team: TeamType) => ({
                            value: team.id,
                            label: team.name,
                          }))}
                          {...field}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="driver"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Who works its issues</FormLabel>
                      <FormControl>
                        <Select
                          value={field.value}
                          onValueChange={(value: string) =>
                            onDriverChange(value as AgentDriver)
                          }
                        >
                          <SelectTrigger className="flex gap-1 items-center">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              <SelectItem value="runtime">
                                Converge runtime
                              </SelectItem>
                              <SelectItem value="external">
                                External tool (MCP or API)
                              </SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </FormControl>
                      <div className="text-muted-foreground text-xs">
                        {DRIVER_HINT[driver]}
                      </div>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="access"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Token access</FormLabel>
                      <FormControl>
                        <Select
                          value={field.value}
                          onValueChange={(value: string) => {
                            field.onChange(value);
                            form.clearErrors('scopes');
                          }}
                        >
                          <SelectTrigger className="flex gap-1 items-center">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              <SelectItem value="scoped">
                                Only the scopes below (recommended)
                              </SelectItem>
                              <SelectItem value="full">Every scope</SelectItem>
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </FormControl>
                    </FormItem>
                  )}
                />

                {access === 'scoped' && (
                  <FormField
                    control={form.control}
                    name="scopes"
                    render={({ field }) => (
                      <FormItem>
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                          {AGENT_SCOPES.map((scope) => (
                            <label
                              key={scope.value}
                              className="flex items-start gap-2 text-sm cursor-pointer"
                            >
                              <Checkbox
                                className="mt-0.5"
                                checked={field.value.includes(scope.value)}
                                onCheckedChange={(on) =>
                                  field.onChange(
                                    on
                                      ? [...field.value, scope.value]
                                      : field.value.filter(
                                          (s: string) => s !== scope.value,
                                        ),
                                  )
                                }
                              />
                              <span className="flex flex-col">
                                <span className="font-mono">{scope.value}</span>
                                <span className="text-muted-foreground text-xs">
                                  {scope.hint}
                                </span>
                              </span>
                            </label>
                          ))}
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}

                <FormField
                  control={form.control}
                  name="teamLimited"
                  render={({ field }) => (
                    <FormItem>
                      <label className="flex items-center gap-2 text-sm cursor-pointer">
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={(on) => {
                            field.onChange(on === true);
                            form.clearErrors('scopes');
                          }}
                        />
                        Limit the token to the teams above
                      </label>
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="expiryHours"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Token expires after</FormLabel>
                      <FormControl>
                        <Select
                          value={String(field.value)}
                          onValueChange={(value: string) =>
                            field.onChange(Number(value))
                          }
                        >
                          <SelectTrigger className="flex gap-1 items-center">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectGroup>
                              {EXPIRY_OPTIONS.map((option) => (
                                <SelectItem
                                  key={option.hours}
                                  value={String(option.hours)}
                                >
                                  {option.label}
                                </SelectItem>
                              ))}
                            </SelectGroup>
                          </SelectContent>
                        </Select>
                      </FormControl>
                    </FormItem>
                  )}
                />

                <div className="flex items-end gap-2 justify-end w-full mt-2">
                  <Button variant="ghost" type="button" onClick={onClose}>
                    Cancel
                  </Button>
                  <Button
                    variant="secondary"
                    type="submit"
                    isLoading={isPending}
                  >
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
