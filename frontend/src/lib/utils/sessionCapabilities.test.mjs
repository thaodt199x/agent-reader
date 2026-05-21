import assert from 'node:assert/strict';
import test from 'node:test';

import { findSession, readOnlySessionLabel, sessionSupportsRPC } from './sessionCapabilities.js';

test('pi sessions support RPC chat', () => {
  assert.equal(sessionSupportsRPC({ id: 'p1', agent: 'pi' }), true);
});

test('sessions without an agent default to pi RPC support', () => {
  assert.equal(sessionSupportsRPC({ id: 'old-session' }), true);
});

test('claude sessions do not support RPC chat', () => {
  assert.equal(sessionSupportsRPC({ id: 'c1', agent: 'claude' }), false);
});

test('codex sessions do not support RPC chat', () => {
  assert.equal(sessionSupportsRPC({ id: 'cx1', agent: 'codex' }), false);
});

test('codex sessions have codex read-only label', () => {
  assert.equal(readOnlySessionLabel({ id: 'cx1', agent: 'codex' }), 'Codex sessions are read-only here');
});

test('finds sessions by id from the session list', () => {
  const session = findSession([
    { id: 'p1', agent: 'pi' },
    { id: 'c1', agent: 'claude' },
  ], 'c1');

  assert.deepEqual(session, { id: 'c1', agent: 'claude' });
});
