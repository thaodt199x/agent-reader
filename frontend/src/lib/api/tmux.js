export async function fetchTmuxSessions(sessionId = '', project = '', cwd = '') {
  const params = new URLSearchParams();
  if (sessionId) params.append('session_id', sessionId);
  if (project) params.append('project', project);
  if (cwd) params.append('cwd', cwd);

  const queryString = params.toString();
  const url = queryString ? `/api/tmux/sessions?${queryString}` : '/api/tmux/sessions';

  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`Failed to fetch tmux sessions: ${res.status}`);
  }
  return res.json();
}

export async function fetchTmuxWindows(sessionName) {
  const res = await fetch(`/api/tmux/sessions/${encodeURIComponent(sessionName)}/windows`);
  if (!res.ok) {
    throw new Error(`Failed to fetch tmux windows: ${res.status}`);
  }
  return res.json();
}
