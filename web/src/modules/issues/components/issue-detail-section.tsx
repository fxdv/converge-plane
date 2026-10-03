import { cn } from '@converge/ui/lib/utils';
import * as React from 'react';

/** Shared vertical rhythm for issue detail blocks (Phase 2). */
export function IssueDetailSection({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return <section className={cn('mt-6 px-6', className)}>{children}</section>;
}
