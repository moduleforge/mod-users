import { describe, expect, test } from 'bun:test';
import { isSafeReturnPath } from './return-path';

describe('isSafeReturnPath', () => {
  const accepted = ['/', '/open/3', '/a?b=c'];
  const rejected: Array<string | null> = [
    null,
    '',
    '//evil.test',
    '/\\evil',
    'https://x',
    'javascript:x',
    '/a\tb',
    '/x:y/z',
  ];

  test.each(accepted)('accepts %p', (candidate) => {
    expect(isSafeReturnPath(candidate)).toBe(true);
  });

  test.each(rejected)('rejects %p', (candidate) => {
    expect(isSafeReturnPath(candidate)).toBe(false);
  });
});
