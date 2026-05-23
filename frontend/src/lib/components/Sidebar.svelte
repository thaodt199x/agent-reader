<script>
  import { sessions, activeSession, unreadSessionIds } from '$lib/stores/session.svelte.js';
  import { sidebarOpen, groupByProject, sortBy } from '$lib/stores/ui.svelte.js';
  import { selectSession } from '$lib/actions/session.js';
  import { tmuxSessionPickerOpen } from '$lib/stores/tmux.svelte.js';
  import { Zap, FolderOpen, List, Clock, Type, Plus, X, ChevronDown, ChevronRight, Terminal } from '@lucide/svelte';

  let { onNewSession } = $props();

  let expandedProjects = $state({});
  let projectSessionLimits = $state({});

  const getProjectLimit = (cwd) => projectSessionLimits[cwd] || 20;

  function loadMoreSessions(cwd) {
    projectSessionLimits[cwd] = getProjectLimit(cwd) + 20;
  }

  // Computed sorted flat sessions list
  let sortedSessions = $derived.by(() => {
    const list = [...$sessions];
    if ($sortBy === 'alphabetical') {
      list.sort((a, b) => {
        const nameA = a.project || a.cwd || '';
        const nameB = b.project || b.cwd || '';
        return nameA.localeCompare(nameB);
      });
    } else {
      // 'last_updated'
      list.sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));
    }
    return list;
  });

  // Computed grouped sessions: sorted by last updated or alphabetically, sessions within sorted by timestamp (newest first)
  let groupedSessions = $derived.by(() => {
    const list = $sessions;
    const groups = {};
    for (const session of list) {
      const key = session.cwd || session.project || 'unknown';
      if (!groups[key]) {
        groups[key] = [];
      }
      groups[key].push(session);
    }

    const mapped = Object.keys(groups).map(cwd => {
      const sorted = groups[cwd].sort((a, b) => new Date(b.timestamp) - new Date(a.timestamp));
      const unreadCount = sorted.filter(s => $unreadSessionIds.has(s.id)).length;
      return {
        cwd,
        sessions: sorted,
        unreadCount,
        newestTimestamp: sorted.length > 0 ? new Date(sorted[0].timestamp) : new Date(0)
      };
    });

    if ($sortBy === 'alphabetical') {
      mapped.sort((a, b) => a.cwd.localeCompare(b.cwd));
    } else {
      // 'last_updated'
      mapped.sort((a, b) => b.newestTimestamp - a.newestTimestamp);
    }

    return mapped;
  });

  // Auto-expand group containing active session
  $effect(() => {
    const activeId = $activeSession;
    if (activeId) {
      for (const group of groupedSessions) {
        if (group.sessions.some(s => s.id === activeId)) {
          expandedProjects[group.cwd] = true;
          break;
        }
      }
    }
  });

  function toggleProjectGroup(cwd) {
    expandedProjects[cwd] = !expandedProjects[cwd];
  }

  function openTmuxPicker() {
    tmuxSessionPickerOpen.set(true);
  }
</script>

{#snippet sessionItem(session)}
  <div
    class="session-item px-4 py-2.5 border-b border-ctp-surface0 cursor-pointer transition-colors duration-150 hover:bg-ctp-surface1 {$activeSession === session.id ? 'active' : ''}"
    onclick={() => selectSession(session.id)}
  >
    <div class="flex items-center justify-between">
      <div class="flex items-center gap-1.5">
        <span class="w-[8px] h-[8px] rounded-full flex-shrink-0 {session.status === 'running' ? 'bg-ctp-green animate-pulse' : session.status === 'error' ? 'bg-ctp-red' : 'bg-ctp-overlay0'}" style="{session.status === 'running' ? 'animation-duration: 1s' : ''}"></span>

        <div class="text-xs {$unreadSessionIds.has(session.id) ? 'font-bold' : 'text-ctp-text'} truncate" style={$unreadSessionIds.has(session.id) ? 'color: var(--color-ctp-maroon)' : ''}>{session.project}</div>
      </div>
      {#if session.last_message_time}
        <div class="text-[10px] text-ctp-overlay0">{session.last_message_time}</div>
      {/if}
    </div>
    {#if session.first_user_message}
      <div class="text-[11px] {$unreadSessionIds.has(session.id) ? 'font-bold' : 'text-ctp-overlay1'} truncate" title={session.first_user_message} style={$unreadSessionIds.has(session.id) ? 'color: var(--color-ctp-maroon)' : ''}>{session.first_user_message}</div>
    {:else}
      <div class="text-[11px] {$unreadSessionIds.has(session.id) ? 'font-bold' : 'text-ctp-overlay1'} break-all" style={$unreadSessionIds.has(session.id) ? 'color: var(--color-ctp-maroon)' : ''}>{session.id}</div>
    {/if}
    <div class="text-[10px] text-ctp-overlay0 mt-0.5">{session.cwd}</div>
    <div class="flex items-center gap-2 mt-0.5">
      <span class="text-[9px] font-semibold px-1.5 py-0.5 rounded bg-ctp-mauve/20 text-ctp-mauve">{session.agent || 'pi'}</span>
      {#if session.model}
        <span class="text-[10px] text-ctp-blue">{session.model}</span>
      {/if}
    </div>
  </div>
{/snippet}

<div class="w-[280px] h-full bg-ctp-mantle border-r border-ctp-surface0 flex flex-col">
  <div class="p-4 border-b border-ctp-surface0 text-sm font-semibold text-ctp-blue flex items-center justify-between" style="background:color-mix(in srgb, #135ce0 4%, #ffffff)">
    <span class="flex items-center gap-1.5"><Zap size={14} /> Sessions{#if $unreadSessionIds.size > 0}<span class="ml-1 text-[10px] font-bold px-1.5 py-0.5 rounded-full bg-ctp-rosewater text-ctp-mantle">{$unreadSessionIds.size}</span>{/if}</span>
    <div class="flex items-center gap-1">
      <button
        class="text-ctp-green hover:text-ctp-teal flex items-center justify-center p-1 rounded hover:bg-ctp-surface0/50 cursor-pointer"
        onclick={() => groupByProject.update(v => !v)}
        title={$groupByProject ? "Switch to flat list" : "Group by project"}
      >
        {#if $groupByProject}
          <List size={14} />
        {:else}
          <FolderOpen size={14} />
        {/if}
      </button>
      <button
        class="text-ctp-green hover:text-ctp-teal flex items-center justify-center p-1 rounded hover:bg-ctp-surface0/50 cursor-pointer"
        onclick={() => sortBy.update(s => s === 'last_updated' ? 'alphabetical' : 'last_updated')}
        title={$sortBy === 'last_updated' ? "Sort: Last Updated" : "Sort: A-Z"}
      >
        {#if $sortBy === 'last_updated'}
          <Clock size={14} />
        {:else}
          <Type size={14} />
        {/if}
      </button>
      <button
        class="text-ctp-green hover:text-ctp-teal flex items-center justify-center p-1 rounded hover:bg-ctp-surface0/50 cursor-pointer"
        onclick={onNewSession}
        title="New Session"
      >
        <Plus size={14} />
      </button>
      <button
        class="text-ctp-green hover:text-ctp-teal flex items-center justify-center p-1 rounded hover:bg-ctp-surface0/50 cursor-pointer"
        onclick={openTmuxPicker}
        title="Connect to tmux session"
      >
        <Terminal size={14} />
      </button>
      <button
        class="md:hidden text-ctp-overlay0 hover:text-ctp-text flex items-center justify-center p-1 rounded hover:bg-ctp-surface0/50 cursor-pointer"
        onclick={() => sidebarOpen.set(false)}
      >
        <X size={14} />
      </button>
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if $sessions.length === 0}
      <div class="flex items-center justify-center h-full text-ctp-overlay0 text-sm">
        No sessions yet
      </div>
    {:else if $groupByProject}
      <!-- Grouped by cwd -->
      {#each groupedSessions as { cwd, sessions: cwdSessions, unreadCount } (cwd)}
        <div class="project-group">
          <button
            class="w-full px-4 py-2 text-xs font-semibold text-ctp-subtext0 flex items-center justify-between hover:bg-ctp-surface1 cursor-pointer border-b border-ctp-surface0"
            onclick={() => toggleProjectGroup(cwd)}
          >
            <span class="truncate">{cwd} ({cwdSessions.length})</span>
            <span class="ml-2 flex-shrink-0 flex items-center">
              {#if expandedProjects[cwd]}
                <ChevronDown size={14} />
              {:else}
                <ChevronRight size={14} />
              {/if}
            </span>
          </button>
          {#if expandedProjects[cwd]}
            {#each cwdSessions.slice(0, getProjectLimit(cwd)) as session (session.id)}
              {@render sessionItem(session)}
            {/each}
            {#if cwdSessions.length > getProjectLimit(cwd)}
              <button
                class="w-full text-center py-2 text-[11px] text-ctp-blue hover:bg-ctp-surface1 cursor-pointer transition-colors duration-150 border-b border-ctp-surface0 font-medium"
                onclick={() => loadMoreSessions(cwd)}
              >
                Load More ({cwdSessions.length - getProjectLimit(cwd)} remaining)
              </button>
            {/if}
          {/if}
        </div>
      {/each}
    {:else}
      <!-- Flat list -->
      {#each sortedSessions as session (session.id)}
        {@render sessionItem(session)}
      {/each}
    {/if}
  </div>
</div>

<style>
  .session-item.active {
    background-color: var(--color-ctp-rosewater) !important;
    border-left: 3px solid var(--color-ctp-red) !important;
  }
</style>
