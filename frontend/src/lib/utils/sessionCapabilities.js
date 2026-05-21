export function findSession(sessions, sessionId) {
  return (sessions || []).find((session) => session.id === sessionId) || null;
}

export function sessionSupportsRPC(session) {
  return (session?.agent || 'pi') === 'pi';
}

export function readOnlySessionLabel(session) {
  switch (session?.agent) {
    case 'claude':
      return 'Claude Code sessions are read-only here';
    case 'codex':
      return 'Codex sessions are read-only here';
    default:
      return 'This session is read-only here';
  }
}
