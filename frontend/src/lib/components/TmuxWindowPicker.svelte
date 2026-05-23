<script>
  import { tmuxWindowPickerOpen, tmuxTerminalTarget, tmuxSessionPickerOpen } from '$lib/stores/tmux.svelte.js';
  import { fetchTmuxWindows } from '$lib/api/tmux.js';
  import { activeSession, sessions as appSessions } from '$lib/stores/session.svelte.js';
  import { findSession } from '$lib/utils/sessionCapabilities.js';
  import { Terminal, X, ArrowLeft, ArrowRight } from '@lucide/svelte';

  let sessionName = $state('');
  let windows = $state([]);
  let loading = $state(false);
  let error = $state('');

  let activeSessionInfo = $derived(findSession($appSessions, $activeSession));
  let projectDir = $derived(activeSessionInfo?.cwd || '');

  function pathContains(projectDir, path) {
    if (!path || !projectDir) return false;
    const p1 = path.replace(/\/$/, '');
    const p2 = projectDir.replace(/\/$/, '');
    return p1 === p2 || p1.startsWith(p2 + '/');
  }

  let filteredWindows = $derived.by(() => {
    if (!projectDir) return windows;
    return windows.filter(win => {
      const sessionId = activeSessionInfo?.id;
      const projectName = activeSessionInfo?.project;
      if (sessionId && win.name && win.name.toLowerCase().includes(sessionId.toLowerCase())) return true;
      if (projectName && win.name && win.name.toLowerCase().includes(projectName.toLowerCase())) return true;
      return pathContains(projectDir, win.path);
    });
  });

  async function loadWindows() {
    loading = true;
    error = '';
    try {
      windows = await fetchTmuxWindows(sessionName);
    } catch (e) {
      error = e.message;
    } finally {
      loading = false;
    }
  }

  function close() {
    tmuxWindowPickerOpen.set(false);
    tmuxSessionPickerOpen.set(true);
  }

  function connect(windowIndex) {
    tmuxWindowPickerOpen.set(false);
    tmuxTerminalTarget.set({ session: sessionName, window: windowIndex });
  }

  $effect(() => {
    if ($tmuxWindowPickerOpen) {
      sessionName = $tmuxWindowPickerOpen;
      loadWindows();
    }
  });
</script>

{#if $tmuxWindowPickerOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" onclick={close}></div>
    <div class="relative bg-ctp-mantle border border-ctp-surface0 rounded-2xl shadow-2xl w-[480px] max-w-[90vw] max-h-[70vh] animate-fadeIn overflow-hidden flex flex-col">
      <!-- Header -->
      <div class="px-6 pt-5 pb-4 border-b border-ctp-surface0">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-3">
            <button
              class="text-ctp-overlay0 hover:text-ctp-text transition-colors p-1 rounded-md hover:bg-ctp-surface0 flex items-center justify-center cursor-pointer"
              onclick={close}
            >
              <ArrowLeft size={16} />
            </button>
            <div class="w-8 h-8 rounded-lg bg-ctp-green/20 flex items-center justify-center text-ctp-green">
              <Terminal size={16} />
            </div>
            <div>
              <h3 class="text-sm font-semibold text-ctp-text">Choose window</h3>
              <p class="text-[11px] text-ctp-overlay0 mt-0.5 font-mono">{sessionName}</p>
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
            Loading windows...
          </div>
        {:else if error}
          <div class="flex items-center gap-2 px-3 py-3 rounded-lg text-xs text-ctp-red"
               style="background:color-mix(in srgb, #e95f59 10%, #ffffff)">
            <span>{error}</span>
          </div>
        {:else if filteredWindows.length === 0}
          <div class="text-center py-8 text-ctp-overlay0 text-sm">
            No matching windows found
          </div>
        {:else}
          <div class="space-y-2">
            {#each filteredWindows as win (win.index)}
              <button
                class="w-full flex items-center justify-between px-4 py-3 bg-ctp-crust border border-ctp-surface0 rounded-lg hover:border-ctp-surface1 transition-colors cursor-pointer text-left"
                onclick={() => connect(win.index)}
              >
                <div class="flex items-center gap-3">
                  <span class="w-[28px] h-[28px] rounded-md bg-ctp-green/20 flex items-center justify-center text-ctp-green font-mono text-sm font-bold">
                    {win.index}
                  </span>
                  <div>
                    <div class="text-sm font-medium text-ctp-text">
                      {win.name || 'window ' + win.index}
                    </div>
                    <div class="text-[11px] text-ctp-overlay0">
                      {win.panes}p
                      {#if win.active}<span class="text-ctp-green ml-1"> active</span>{/if}
                    </div>
                  </div>
                </div>
                <ArrowRight size={14} class="text-ctp-overlay0" />
              </button>
            {/each}
          </div>
        {/if}
      </div>

      <!-- Footer -->
      <div class="px-6 py-3 border-t border-ctp-surface0 flex justify-between items-center">
        <span class="text-[11px] text-ctp-overlay0">{filteredWindows.length} window{filteredWindows.length !== 1 ? 's' : ''}</span>
        <button
          class="flex items-center gap-1 px-3 py-1.5 rounded-md text-xs font-medium text-ctp-overlay0 hover:text-ctp-text hover:bg-ctp-surface0 transition-colors cursor-pointer"
          onclick={loadWindows}
          disabled={loading}
        >
          Refresh
        </button>
      </div>
    </div>
  </div>
{/if}
