<script>
  import { tmuxSessionPickerOpen, tmuxTerminalTarget, tmuxWindowPickerOpen } from '$lib/stores/tmux.svelte.js';
  import { fetchTmuxSessions } from '$lib/api/tmux.js';
  import { activeSession, sessions as appSessions } from '$lib/stores/session.svelte.js';
  import { findSession } from '$lib/utils/sessionCapabilities.js';
  import { Terminal, X, RefreshCw, ArrowRight } from '@lucide/svelte';

  let sessions = $state([]);
  let loading = $state(false);
  let error = $state('');
  let available = $state(true);

  let activeSessionInfo = $derived(findSession($appSessions, $activeSession));
  let projectDir = $derived(activeSessionInfo?.cwd || '');

  function pathContains(projectDir, path) {
    if (!path || !projectDir) return false;
    const p1 = path.replace(/\/$/, '');
    const p2 = projectDir.replace(/\/$/, '');
    return p1 === p2 || p1.startsWith(p2 + '/');
  }

  function sessionMatches(session, projectDir, activeSessionInfo) {
    if (session.related) return true;

    if (activeSessionInfo) {
      const sessionId = activeSessionInfo.id;
      const projectName = activeSessionInfo.project;

      if (sessionId) {
        if (session.name.toLowerCase().includes(sessionId.toLowerCase())) return true;
        if (session.window_list && session.window_list.some(win => win.name && win.name.toLowerCase().includes(sessionId.toLowerCase()))) return true;
      }

      if (projectName) {
        if (session.name.toLowerCase().includes(projectName.toLowerCase())) return true;
        if (session.window_list && session.window_list.some(win => win.name && win.name.toLowerCase().includes(projectName.toLowerCase()))) return true;
      }
    }

    if (!projectDir) return true;
    if (pathContains(projectDir, session.path)) return true;
    if (session.window_list && session.window_list.length > 0) {
      return session.window_list.some(win => pathContains(projectDir, win.path));
    }
    const p1 = session.path.replace(/\/$/, '');
    const p2 = projectDir.replace(/\/$/, '');
    return p2.startsWith(p1 + '/');
  }

  let filteredSessions = $derived.by(() => {
    if (!activeSessionInfo) return sessions;
    return sessions.filter(session => sessionMatches(session, projectDir, activeSessionInfo));
  });

  async function loadSessions() {
    loading = true;
    error = '';
    try {
      const data = await fetchTmuxSessions(
        activeSessionInfo?.id || '',
        activeSessionInfo?.project || '',
        activeSessionInfo?.cwd || ''
      );
      available = data.available;
      sessions = data.sessions || [];
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function close() {
    tmuxSessionPickerOpen.set(false);
  }

  function connect(session) {
    tmuxSessionPickerOpen.set(false);
    if (session.windows <= 1) {
      tmuxTerminalTarget.set({ session: session.name, window: 0 });
    } else {
      tmuxWindowPickerOpen.set(session.name);
    }
  }

  $effect(() => {
    if ($tmuxSessionPickerOpen) {
      loadSessions();
    }
  });
</script>

{#if $tmuxSessionPickerOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={close}></div>
    <div class="relative bg-ctp-mantle border border-ctp-surface0 rounded-2xl shadow-2xl w-[480px] max-w-[90vw] max-h-[70vh] animate-fadeIn overflow-hidden flex flex-col">
      <!-- Header -->
      <div class="px-6 pt-5 pb-4 border-b border-ctp-surface0">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-3">
            <div class="w-8 h-8 rounded-lg bg-ctp-green/20 flex items-center justify-center text-ctp-green">
              <Terminal size={16} />
            </div>
            <div>
              <h3 class="text-sm font-semibold text-ctp-text">Connect to tmux</h3>
              <p class="text-[11px] text-ctp-overlay0 mt-0.5">Attach to a running tmux session</p>
            </div>
          </div>
          <button
            class="text-ctp-overlay0 hover:text-ctp-text transition-colors p-1 rounded-md hover:bg-ctp-surface0 flex items-center justify-center cursor-pointer"
            onclick={close}
          >
            <X class="h-4 w-4" />
          </button>
        </div>
      </div>

      <!-- Body -->
      <div class="px-6 py-4 flex-1 overflow-y-auto">
        {#if loading}
          <div class="flex items-center justify-center py-8 text-ctp-overlay0 text-sm">
            Loading sessions...
          </div>
        {:else if !available}
          <div class="flex items-center gap-2 px-3 py-3 rounded-lg text-xs"
               style="background:color-mix(in srgb, #e95f59 10%, #ffffff); color: var(--color-ctp-red)">
            <span>tmux is not installed on this machine.</span>
          </div>
        {:else if error}
          <div class="flex items-center gap-2 px-3 py-3 rounded-lg text-xs text-ctp-red"
               style="background:color-mix(in srgb, #e95f59 10%, #ffffff)">
            <span>{error}</span>
          </div>
        {:else if filteredSessions.length === 0}
          <div class="text-center py-8 text-ctp-overlay0 text-sm">
            No matching tmux sessions found
          </div>
        {:else}
          <div class="space-y-2">
            {#each filteredSessions as session (session.name)}
              <div class="flex items-center justify-between px-4 py-3 bg-ctp-crust border border-ctp-surface0 rounded-lg">
                <div class="flex items-center gap-3">
                  <span class="w-[8px] h-[8px] rounded-full flex-shrink-0 {session.attached ? 'bg-ctp-green' : 'bg-ctp-overlay0'}"></span>
                  <div>
                    <div class="text-sm font-medium text-ctp-text">{session.name}</div>
                    <div class="text-[11px] text-ctp-overlay0">
                      {session.windows}w / {session.panes}p
                      {#if session.attached}<span class="text-ctp-green ml-1"> attached</span>{/if}
                    </div>
                  </div>
                </div>
                <button
                  class="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium bg-ctp-green/20 text-ctp-green hover:bg-ctp-green/30 transition-colors cursor-pointer"
                  onclick={() => connect(session)}
                >
                  Connect <ArrowRight size={12} />
                </button>
              </div>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="px-6 py-3 border-t border-ctp-surface0 flex justify-between items-center">
        <span class="text-[11px] text-ctp-overlay0">{filteredSessions.length} session{filteredSessions.length !== 1 ? 's' : ''}</span>
        <button
          class="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-ctp-overlay0 hover:text-ctp-text hover:bg-ctp-surface0 transition-colors cursor-pointer"
          onclick={loadSessions}
          disabled={loading}
        >
          <RefreshCw size={12} class={loading ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>
    </div>
  </div>
{/if}
