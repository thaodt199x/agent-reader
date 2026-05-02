<script>
  import { sessions, activeSession } from '$lib/stores/session.svelte.js';
  import { sidebarOpen, groupByProject } from '$lib/stores/ui.svelte.js';
  import { selectSession } from '$lib/actions/session.js';

  let { onNewSession } = $props();
</script>

<div class="sidebar w-[280px] h-full bg-ctp-mantle border-r border-ctp-surface0 flex flex-col">
  <div class="p-4 border-b border-ctp-surface0 text-sm font-semibold text-ctp-blue flex items-center justify-between">
    <span>⚡ Sessions</span>
    <div class="flex items-center gap-2">
      <button
        class="text-ctp-green hover:text-ctp-teal text-xs font-bold"
        onclick={() => groupByProject.update(v => !v)}
        title={$groupByProject ? "Switch to flat list" : "Group by project"}
      >{$groupByProject ? '📁' : '≡'}</button>
      <button
        class="text-ctp-green hover:text-ctp-teal text-xs font-bold"
        onclick={onNewSession}
        title="New Session"
      >＋</button>
      <button
        class="md:hidden text-ctp-overlay0 hover:text-ctp-text"
        onclick={() => sidebarOpen.set(false)}
      >✕</button>
    </div>
  </div>

  <div class="flex-1 overflow-y-auto">
    {#if $sessions.length === 0}
      <div class="flex items-center justify-center h-full text-ctp-overlay0 text-sm">
        No sessions yet
      </div>
    {:else}
      {#each $sessions as session (session.id)}
        <div
          class="session-item px-4 py-2.5 border-b border-ctp-surface0 cursor-pointer transition-colors duration-150 hover:bg-ctp-surface1 {$activeSession === session.id ? 'bg-ctp-surface0 border-l-[3px] border-ctp-blue' : ''}"
          onclick={() => selectSession(session.id)}
        >
          <div class="flex items-center justify-between">
            <div class="text-xs text-ctp-text">{session.project}</div>
            {#if session.last_message_time}
              <div class="text-[10px] text-ctp-overlay0">{session.last_message_time}</div>
            {/if}
          </div>
          <div class="text-[11px] text-ctp-overlay1 break-all">{session.id}</div>
          <div class="text-[10px] text-ctp-overlay0 mt-0.5">{session.cwd}</div>
          {#if session.model}
            <div class="text-[10px] text-ctp-blue mt-0.5">{session.model}</div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>
</div>
