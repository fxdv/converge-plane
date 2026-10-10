import { Button } from '@converge/ui/components/button';
import { NoPriorityLine } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { observer } from 'mobx-react-lite';
import { AutoSizer, type Index, type ListRowProps } from 'react-virtualized';

import { IssueListItem, PriorityIcons } from 'modules/issues/components';
import { useFilterIssues } from 'modules/issues/issues-utils';

import { ScrollManagedList } from 'components/scroll-managed-list';
import { usePriorities } from 'hooks/priorities';
import { useCurrentTeam } from 'hooks/teams';
import { useComputedWorkflows } from 'hooks/workflows';

import { useContextStore } from 'store/global-context-provider';

import { MeasuredRow, useMeasuredRowCache } from '../../board-measured-row';
import { useIssueRowsPriority } from './utils';

export const PriorityList = observer(() => {
  const team = useCurrentTeam();
  const Priorities = usePriorities();

  const { issuesStore } = useContextStore();
  const issues = issuesStore.getIssues({
    teamId: team?.id,
  });
  const { workflows } = useComputedWorkflows();
  const filteredIssues = useFilterIssues(issues, workflows);

  const rows = useIssueRowsPriority(filteredIssues, Priorities);
  const cache = useMeasuredRowCache(
    rows
      .map((row) =>
        row.type === 'header' ? `h:${row.key}` : `i:${row.issueId}`,
      )
      .join('\0'),
    45,
  );

  const getHeaderRow = (row: { type: string; key: string }, index: number) => {
    if (!row) {
      return null;
    }
    let childContent;

    if (row.key === 'no-value') {
      childContent = (
        <>
          <NoPriorityLine className="h-5 w-5" />
        </>
      );
    } else {
      const PriorityIcon = PriorityIcons[parseInt(row.key, 10)];
      childContent = (
        <>
          <PriorityIcon.icon size={20} />
          <h3 className="pl-2">{Priorities[parseInt(row.key, 10)]}</h3>
        </>
      );
    }

    return (
      <Button
        className={cn(
          'flex group items-center ml-4 w-fit rounded-2xl bg-grayAlpha-100 mb-2 cursor-default',
          index !== 0 && 'mt-4',
        )}
        variant="ghost"
        size="lg"
      >
        {childContent}
      </Button>
    );
  };

  const rowRender = ({ index, style, key, parent }: ListRowProps) => {
    const row = rows[index];

    if (!row) {
      return null;
    }

    return (
      <MeasuredRow
        cache={cache}
        parent={parent}
        index={index}
        rowKey={key}
        style={style}
      >
        {(measure) =>
          row.type === 'header' ? (
            getHeaderRow(row, index)
          ) : (
            <IssueListItem issueId={row.issueId} measure={measure} />
          )
        }
      </MeasuredRow>
    );
  };

  const rowHeight = ({ index }: Index) => {
    const row = rows[index];

    if (row && row.type === 'issue') {
      const defaultHeight = row.hasRelations ? 73 : 45;

      return Math.max(cache.getHeight(index, 0), defaultHeight);
    }

    return cache.getHeight(index, 0);
  };

  return (
    <AutoSizer className="h-full">
      {({ width, height }) => (
        <ScrollManagedList
          className=""
          listId="category-list"
          height={height}
          overscanRowCount={10}
          noRowsRenderer={() => <></>}
          rowCount={rows.length + 2}
          rowHeight={rowHeight}
          rowRenderer={rowRender}
          width={width}
          shallowCompare
        />
      )}
    </AutoSizer>
  );
});
