/** Board column virtualization (Phase 4 / OD-15). */
export const BOARD_OVERSCAN_ROW_COUNT = 5;

export const BOARD_DEFAULT_ROW_HEIGHT = 100;

export function boardVirtualRowCount(
  visibleCount: number,
  isUsingPlaceholder: boolean,
): number {
  return isUsingPlaceholder ? visibleCount + 1 : visibleCount;
}
