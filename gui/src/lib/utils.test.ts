import { describe, expect, test } from 'bun:test';
import { cn } from './utils';

describe('cn', () => {
  test('joins plain string classes with a space', () => {
    expect(cn('a', 'b', 'c')).toBe('a b c');
  });

  test('drops falsy values (undefined, null, false, empty string)', () => {
    expect(cn('a', undefined, null, false, '', 'b')).toBe('a b');
  });

  test('flattens arrays and objects of classes (clsx semantics)', () => {
    expect(cn(['a', 'b'], { c: true, d: false })).toBe('a b c');
  });

  test('lets a later conflicting Tailwind utility win (tailwind-merge semantics)', () => {
    // `p-2` and `p-4` are both "padding" utilities targeting the same
    // property, so tailwind-merge should keep only the last one rather
    // than emitting both (which is what plain string concatenation would
    // do and what makes twMerge worth having over clsx alone).
    expect(cn('p-2', 'p-4')).toBe('p-4');
  });

  test('merges a base class list with a conditionally-applied override, matching component usage', () => {
    // Mirrors how ui/table.tsx etc. call cn(baseClasses, className) to let
    // a caller-supplied className override one of the fixed base classes.
    expect(cn('w-full text-sm', 'text-lg')).toBe('w-full text-lg');
  });
});
