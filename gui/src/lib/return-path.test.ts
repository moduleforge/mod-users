import { afterEach, describe, expect, test } from 'bun:test';
import { configureUsersApi, resetUsersApiConfig } from './config';
import { isSafeReturnPath, readReturnPath } from './return-path';

const realLocationDescriptor = Object.getOwnPropertyDescriptor(window, 'location');

/** Replaces window.location with a stub carrying `search`; `assign` records. */
function stubLocation(search: string, assigned: string[] = []): void {
  Object.defineProperty(window, 'location', {
    value: {
      pathname: '/auth/login',
      search,
      hash: '',
      origin: 'http://app.test',
      href: `http://app.test/auth/login${search}`,
      assign: (url: string) => {
        assigned.push(url);
      },
    },
    configurable: true,
    writable: true,
  });
}

function restoreLocation(): void {
  if (realLocationDescriptor) {
    Object.defineProperty(window, 'location', realLocationDescriptor);
  }
}

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

describe('readReturnPath', () => {
  afterEach(() => {
    restoreLocation();
    resetUsersApiConfig();
  });

  test('returns null when no param is configured', () => {
    stubLocation('?return=/x');
    expect(readReturnPath()).toBeNull();
  });

  test('returns a safe value for the configured param, at call time', () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    stubLocation('?return=/open/3');
    expect(readReturnPath()).toBe('/open/3');
    stubLocation('?return=/other');
    expect(readReturnPath()).toBe('/other');
  });

  test('returns null for a missing or unsafe value', () => {
    configureUsersApi({ unauthenticatedReturnParam: 'return' });
    stubLocation('');
    expect(readReturnPath()).toBeNull();
    stubLocation('?return=%2F%2Fevil.test');
    expect(readReturnPath()).toBeNull();
  });
});
