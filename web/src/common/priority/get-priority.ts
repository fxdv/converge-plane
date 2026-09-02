export const Priorities = ['', 'P0', 'P1', 'P2', 'P3'];

export function getPriorities() {
  return Priorities;
}

// The wire domain is Tegon's: a nullable integer with no range (see
// migration 0008). The display domain is Priorities' 0..4. Out-of-domain
// values (a legacy row can hold 5) must degrade to "no priority" rather
// than crash a render: every PriorityIcons/Priorities lookup goes
// through this.
export function safePriorityIndex(value: number | null | undefined): number {
  if (
    typeof value !== 'number' ||
    !Number.isInteger(value) ||
    value < 0 ||
    value >= Priorities.length
  ) {
    return 0;
  }
  return value;
}
