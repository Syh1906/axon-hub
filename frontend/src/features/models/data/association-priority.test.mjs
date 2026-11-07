import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { MAX_ASSOCIATION_PRIORITY, associationPrioritySchema, hasInvalidAssociationPriority, nextAssociationPriority } from './association-priority.ts';

function firstIssue(value) {
  const result = associationPrioritySchema.safeParse(value);
  return result.success ? null : result.error.issues[0].message;
}

test('association priority accepts the documented 0-10 range', () => {
  assert.equal(MAX_ASSOCIATION_PRIORITY, 10);
  for (const priority of [0, 1, 5, 9, 10]) {
    assert.equal(firstIssue(priority), null, `priority ${priority} should be valid`);
  }
});

test('association priority reports out-of-range values instead of clamping them', () => {
  assert.equal(firstIssue(11), 'Priority cannot exceed 10');
  assert.equal(firstIssue(-1), 'Priority must be at least 0');
});

test('association priority rejects non-integers because the GraphQL field is an Int', () => {
  assert.equal(firstIssue(1.5), 'Priority must be an integer');
  assert.equal(firstIssue(99.9), 'Priority must be an integer');
});

test('association priority requires a value once the input is cleared', () => {
  // The dialog stores null for an empty input; undefined and NaN cannot be
  // produced by a number input but must not slip through either.
  assert.equal(firstIssue(null), 'Priority is required');
  assert.equal(firstIssue(undefined), 'Priority is required');
  assert.equal(firstIssue(Number.NaN), 'Priority is required');
});

test('new rules take the next priority after the current highest one', () => {
  assert.equal(nextAssociationPriority([]), 0);
  assert.equal(nextAssociationPriority([0]), 1);
  assert.equal(nextAssociationPriority([2, 5]), 6);
  assert.equal(nextAssociationPriority([9]), 10);
});

test('new rules saturate at the cap and share priority 10 (same tier)', () => {
  assert.equal(nextAssociationPriority([10]), 10);
  assert.equal(nextAssociationPriority([10, 10]), 10);
  assert.equal(nextAssociationPriority([150]), 10);
});

test('new rules ignore cleared inputs and normalize pending invalid values', () => {
  assert.equal(nextAssociationPriority([null]), 0);
  assert.equal(nextAssociationPriority([3, null, undefined]), 4);
  assert.equal(nextAssociationPriority([1.5]), 2);
  assert.equal(nextAssociationPriority([-5]), 0);
});

test('the preview gate only stops on invalid priorities', () => {
  assert.equal(hasInvalidAssociationPriority([]), false);
  assert.equal(hasInvalidAssociationPriority([0, 6, 10]), false);
  // An empty input is pending, not invalid: the rule itself is incomplete too.
  assert.equal(hasInvalidAssociationPriority([2, null, undefined]), false);
  assert.equal(hasInvalidAssociationPriority([1.5]), true);
  assert.equal(hasInvalidAssociationPriority([11]), true);
  assert.equal(hasInvalidAssociationPriority([-1]), true);
  assert.equal(hasInvalidAssociationPriority([2, 1.5]), true);
});

test('the dialog and the shared model schema agree on the priority cap', () => {
  const componentsDir = join(import.meta.dirname, '..', 'components');
  const dialog = readFileSync(join(componentsDir, 'models-association-dialog.tsx'), 'utf8');
  const sharedSchema = readFileSync(join(import.meta.dirname, 'schema.ts'), 'utf8');

  assert.match(dialog, /priority: associationPrioritySchema,/);
  assert.match(dialog, /priority: nextAssociationPriority\(/);
  // The input must hand the raw value to the schema; clamping would hide the error.
  assert.doesNotMatch(dialog, /Math\.min\(MAX_ASSOCIATION_PRIORITY/);
  // The preview request must be skipped while a priority fails validation.
  assert.match(dialog, /hasInvalidAssociationPriority\(/);
  assert.match(sharedSchema, new RegExp(`priority: z\\.number\\(\\)\\.int\\(\\)\\.min\\(0\\)\\.max\\(${MAX_ASSOCIATION_PRIORITY}\\)`));
});
