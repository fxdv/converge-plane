import { BadgeColor } from '@converge/ui/components/badge';
import { Button } from '@converge/ui/components/button';
import { LabelLine } from '@converge/ui/icons';
import { cn } from '@converge/ui/lib/utils';
import { observer } from 'mobx-react-lite';
import { AutoSizer, type Index, type ListRowProps } from 'react-virtualized';

import { IssueListItem } from 'modules/issues/components';
import { useFilterIssues } from 'modules/issues/issues-utils';

import type { LabelType } from 'common/types';

import { ScrollManagedList } from 'components/scroll-managed-list';
import { useCurrentTeam } from 'hooks/teams';
import { useComputedWorkflows } from 'hooks/workflows';

import { useContextStore } from 'store/global-context-provider';

import { MeasuredRow, useMeasuredRowCache } from '../../board-measured-row';
import { useIssueRowsLabel } from './utils';

interface LabelListProps {
  labels: LabelType[];
}

export const LabelList = observer(({ labels }: LabelListProps) => {
  const team = useCurrentTeam();

  const { issuesStore } = useContextStore();
  const issues = issuesStore.getIssues({
    teamId: team?.id,
  });
  const { workflows } = useComputedWorkflows();
  const filteredIssues = useFilterIssues(issues, workflows);

  const rows = useIssueRowsLabel(filteredIssues, labels);
  const cache = useMeasuredRowCache(
    rows
      .map((row) =>
        row.type === 'header' ? `h:${row.key}` : `i:${row.issueId}`,
      )
      .join('\0'),
    45,
  );

  const getHeaderRow = (row: { type: string; key: string }, index: number) => {
    let childContent;
    if (!row) {
      return null;
    }
    if (row.key === 'no-value') {
      childContent = (
        <>
          <LabelLine className="h-5 w-5" />
          <h3 className="pl-2">No label</h3>
        </>
      );
    } else {
      const label = labels.find((label) => label.id.includes(row.key));

      childContent = (
        <>
          <div className="h-5 w-5 flex items-center justify-center">
            <BadgeColor
              style={{ backgroundColor: label.color }}
              className="w-2 h-2"
            />
          </div>
          <h3 className="pl-1">{label.name}</h3>
        </>
      );
    }

    return (
      <>
        <Button
          className={cn(
            'flex items-center group ml-4 w-fit rounded-2xl bg-grayAlpha-100 mb-2 cursor-default',
            index !== 0 && 'mt-4',
          )}
          variant="ghost"
          size="lg"
        >
          {childContent}
        </Button>
      </>
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
          listId="label-list"
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
