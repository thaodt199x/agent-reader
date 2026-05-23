export async function fetchTmuxSessions() {
  const res = await fetch('/api/tmux/sessions');
  if (!res.ok) {
    throw new Error(`Failed to fetch tmux sessions: ${res.status}`);
  }
  return res.json();
}
