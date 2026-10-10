import * as React from 'react';
import {
  CellMeasurer,
  CellMeasurerCache,
  type ListRowProps,
} from 'react-virtualized';

import { BOARD_DEFAULT_ROW_HEIGHT } from './board-virtual-config';

/**
 * Stable across renders. Cleared only when `signature` changes, so a
 * measurement is not thrown away on the next MobX render or scroll tick.
 */
export function useMeasuredRowCache(signature: string, defaultHeight: number) {
  // Lazy initializer: built once on mount, not discarded on every render.
  const [cache] = React.useState(
    () =>
      new CellMeasurerCache({
        defaultHeight,
        fixedWidth: true,
      }),
  );
  const previous = React.useRef<string | null>(null);
  if (previous.current === null) {
    previous.current = signature;
  } else if (previous.current !== signature) {
    previous.current = signature;
    cache.clearAll();
  }
  return cache;
}

export function useBoardMeasureCache(issueIds: string[]) {
  return useMeasuredRowCache(issueIds.join('\0'), BOARD_DEFAULT_ROW_HEIGHT);
}

/**
 * A cached height of 0 collapses the grid: its inner container is
 * `overflow: hidden` and `max-height` equal to the sum of row heights,
 * so every card in the column disappears. Fall back to the estimate
 * until a real measurement lands.
 */
export function rowHeightOr(cache: CellMeasurerCache, fallback: number) {
  return ({ index }: { index: number }) => {
    const height = cache.rowHeight({ index });
    return height > 0 ? height : fallback;
  };
}

/**
 * The grid positions this element with a pixel height. That height has to
 * be the content height: painting the row taller than the slot just gets
 * clipped by the grid container. Re-measure once the column width is real
 * (the unmeasured placeholder is `width: auto`, which records a collapsed
 * height) and again whenever that slot changes.
 */
function MeasuredRowBox({
  measure,
  style,
  className,
  children,
}: {
  measure: () => void;
  style: React.CSSProperties;
  className?: string;
  children: React.ReactNode;
}) {
  const measureRef = React.useRef(measure);
  measureRef.current = measure;
  const width = typeof style.width === 'number' ? style.width : 0;
  const height = typeof style.height === 'number' ? style.height : 0;

  React.useLayoutEffect(() => {
    if (width <= 0) {
      return;
    }
    measureRef.current();
  }, [width, height]);

  return (
    <div style={style} className={className}>
      {children}
    </div>
  );
}

export function MeasuredRow({
  cache,
  parent,
  index,
  rowKey,
  style,
  className,
  children,
}: {
  cache: CellMeasurerCache;
  parent: ListRowProps['parent'];
  index: number;
  rowKey: string;
  style: React.CSSProperties;
  className?: string;
  children: (measure: () => void) => React.ReactNode;
}) {
  return (
    <CellMeasurer
      cache={cache}
      columnIndex={0}
      key={rowKey}
      parent={parent}
      rowIndex={index}
    >
      {({ measure }) => (
        <MeasuredRowBox measure={measure} style={style} className={className}>
          {children(measure)}
        </MeasuredRowBox>
      )}
    </CellMeasurer>
  );
}

export function BoardMeasuredRow({
  className,
  ...props
}: React.ComponentProps<typeof MeasuredRow>) {
  return <MeasuredRow {...props} className={className ?? 'pb-2'} />;
}
