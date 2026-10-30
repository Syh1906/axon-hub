import type { BudgetPatch, KeyInput, ManagementRow } from './data';

export type Defaults = { budget?: BudgetPatch; keys: KeyInput[] };
export type Draft = { id: string; selected: boolean; name: string; note: string; groupRef: number; overrides?: Defaults };
export const newKey = (name = ''): KeyInput => ({ name, type: 'personal' });
export const newDraft = (groupRef = 0): Draft => ({ id: crypto.randomUUID(), selected: true, name: '', note: '', groupRef });

export function importedGroup(
  cells: string[],
  columnCount: number,
  groupColumn: number,
  targets: number[],
  groups: { id: number; name: string }[],
  ungroupedName: string
): { groupRef: number } | { error: string; name?: string } {
  if (columnCount === 1) {
    return targets.length === 1 ? { groupRef: targets[0] } : { error: 'kg.importTargetRequired' };
  }
  const name = cells[groupColumn]?.trim();
  if (!name) return { error: 'kg.importGroupRequired' };
  const group = groups.find((g) => g.name === name);
  if (group) return { groupRef: group.id };
  if (name === ungroupedName) return { groupRef: 0 };
  return { error: 'kg.unknownGroup', name };
}

export function materialize(drafts: Draft[], defaults: Defaults, groups: boolean): ManagementRow[] {
  return drafts
    .filter((d) => d.selected)
    .map((d) => {
      const settings = d.overrides ?? defaults;
      return groups
        ? { rowID: d.id, action: 'createGroup', name: d.name.trim(), note: d.note, budget: settings.budget, keys: settings.keys }
        : { rowID: d.id, action: 'createKey', groupRef: d.groupRef, keys: [{ ...(settings.keys[0] ?? newKey()), name: d.name.trim() }] };
    });
}
// RFC 4180 quoting also supports Excel's tab-separated clipboard text.
export function parseTable(text: string): string[][] {
  const separator = text.includes('\t') ? '\t' : ',';
  const rows: string[][] = [];
  let row: string[] = [];
  let value = '';
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (c === '"') {
      if (quoted && text[i + 1] === '"') {
        value += '"';
        i++;
      } else quoted = !quoted;
    } else if (!quoted && (c === separator || c === '\n' || c === '\r')) {
      row.push(value);
      value = '';
      if (c !== separator) {
        if (row.some((v) => v.trim())) rows.push(row);
        row = [];
        if (c === '\r' && text[i + 1] === '\n') i++;
      }
    } else value += c;
  }
  if (quoted) throw new Error('unclosedQuote');
  row.push(value);
  if (row.some((v) => v.trim())) rows.push(row);
  return rows;
}
export function csvCell(value: string) {
  return '"' + value.replaceAll('"', '""') + '"';
}
