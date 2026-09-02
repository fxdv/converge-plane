import { Badge } from '@converge/ui/components/badge';
import { Button } from '@converge/ui/components/button';
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@converge/ui/components/dialog';
import { cn } from '@converge/ui/lib/utils';
import * as React from 'react';

import { AGENTS_BRIEFING } from './agents-briefing';

interface AgentsBriefingDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

// The in-app briefing for the agent swarm: what agents are, how they
// coordinate, and when they stop. Content lives in agents-briefing.ts so
// the copy has exactly one home.
export function AgentsBriefingDialog({
  open,
  onOpenChange,
}: AgentsBriefingDialogProps) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>How the swarm works</DialogTitle>
          <DialogDescription>
            What agents are, how they hand work to each other, and when they
            stop.
          </DialogDescription>
        </DialogHeader>

        <div className="max-h-[60vh] space-y-5 overflow-y-auto pr-2">
          {AGENTS_BRIEFING.map((section) => (
            <div key={section.title}>
              <div className="mb-1 flex items-center gap-2">
                <h3 className="text-sm font-semibold">{section.title}</h3>
                <Badge
                  variant="secondary"
                  className={cn(
                    section.status === 'live'
                      ? 'text-emerald-700 dark:text-emerald-400'
                      : 'text-amber-700 dark:text-amber-400',
                  )}
                >
                  {section.status === 'live' ? 'Live' : 'Next'}
                </Badge>
              </div>
              <p className="text-sm leading-relaxed text-muted-foreground">
                {section.body}
              </p>
            </div>
          ))}
        </div>

        <DialogFooter>
          <p className="mr-auto text-xs text-muted-foreground">
            Sections marked Next ship in the next milestone; everything else
            here is live today.
          </p>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            Got it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
