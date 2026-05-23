<script>
  import { onMount } from 'svelte';
  import { connectWS } from '$lib/api/websocket.js';
  import { fetchSessions, fetchUnreadIds } from '$lib/api/sessions.js';
  import { getRPCStatus } from '$lib/api/rpc.js';
  import { activeSession, sessions, unreadSessionIds } from '$lib/stores/session.svelte.js';
  import { userScrolledUp, newMessageCount } from '$lib/stores/messages.svelte.js';
  import { setRpcRunning } from '$lib/stores/rpc.svelte.js';
  import { sidebarOpen, newSessionModalOpen, sortBy, groupByProject } from '$lib/stores/ui.svelte.js';
  import { ws } from '$lib/stores/ws.svelte.js';
  import Sidebar from '$lib/components/Sidebar.svelte';
  import HeaderBar from '$lib/components/HeaderBar.svelte';
  import ChatArea from '$lib/components/ChatArea.svelte';
  import NewSessionModal from '$lib/components/NewSessionModal.svelte';
  import ToastContainer from '$lib/components/ToastContainer.svelte';
  import TmuxSessionPicker from '$lib/components/TmuxSessionPicker.svelte';
  import TmuxTerminalModal from '$lib/components/TmuxTerminalModal.svelte';
  import TmuxWindowPicker from '$lib/components/TmuxWindowPicker.svelte';
  import { tmuxSessionPickerOpen } from '$lib/stores/tmux.svelte.js';
  import { findSession } from '$lib/utils/sessionCapabilities.js';
  import { Terminal } from '@lucide/svelte';

  let isMobile = $state(false);

  onMount(() => {
    // Check if mobile
    isMobile = window.innerWidth <= 768;

    // Listen for resize
    const handleResize = () => {
      isMobile = window.innerWidth <= 768;
    };
    window.addEventListener('resize', handleResize);

    // Connect WebSocket
    connectWS();

    // Subscribe to sortBy and groupByProject to reactively fetch sessions
    let currentSortBy = 'last_updated';
    let currentGroupBy = false;

    function refreshSessions() {
      fetchSessions(currentSortBy, currentGroupBy)
        .then(list => sessions.set(list))
        .catch(e => console.error('Failed to fetch sessions:', e));
      // Refresh unread status (exclude the active session — user is already viewing it)
      fetchUnreadIds()
        .then(ids => {
          const unsub = activeSession.subscribe(activeId => {
            if (activeId) ids.delete(activeId);
          });
          unsub();
          unreadSessionIds.set(ids);
        })
        .catch(() => {});
    }

    const unsubscribeSort = sortBy.subscribe(value => {
      currentSortBy = value;
      refreshSessions();
    });

    const unsubscribeGroup = groupByProject.subscribe(value => {
      currentGroupBy = value;
      refreshSessions();
    });

    // Sync RPC status from server (restores state after page reload)
    getRPCStatus()
      .then(data => {
        if (data.sessions) {
          for (const [sessionId, running] of Object.entries(data.sessions)) {
            if (running) {
              setRpcRunning(sessionId, true);
            }
          }
        }
      })
      .catch(() => {});

    // Re-subscribe to active session on reload (scrolls to bottom)
    let savedSession = null;
    activeSession.subscribe(id => { savedSession = id; })();
    if (savedSession) {
      const trySubscribe = () => {
        let socket = null;
        ws.subscribe(s => { socket = s; })();
        if (socket && socket.readyState === WebSocket.OPEN) {
          socket.send(JSON.stringify({ type: 'subscribe', session_id: savedSession }));
          userScrolledUp.set(false);
          newMessageCount.set(0);
        } else {
          // WS not ready yet, retry after a short delay
          setTimeout(trySubscribe, 200);
        }
      };
      trySubscribe();
    }

    // Refresh sessions periodically
    const interval = setInterval(() => {
      fetchSessions(currentSortBy, currentGroupBy)
        .then(list => sessions.set(list))
        .catch(() => {});
      // Refresh unread status (exclude the active session — user is already viewing it)
      fetchUnreadIds()
        .then(ids => {
          const unsub = activeSession.subscribe(activeId => {
            if (activeId) ids.delete(activeId);
          });
          unsub();
          unreadSessionIds.set(ids);
        })
        .catch(() => {});
    }, 5000);

    return () => {
      clearInterval(interval);
      window.removeEventListener('resize', handleResize);
      unsubscribeSort();
      unsubscribeGroup();
    };
  });

  function showNewSessionModal() {
    newSessionModalOpen.set(true);
  }

  let activeSessionInfo = $derived(findSession($sessions, $activeSession));

  function openTmuxPicker() {
    tmuxSessionPickerOpen.set(true);
  }
</script>

<div class="flex h-screen">
  <!-- Sidebar overlay (mobile) -->
  {#if isMobile}
    <div
      class="fixed inset-0 bg-black/50 z-40"
      class:hidden={!$sidebarOpen}
      onclick={() => sidebarOpen.set(false)}
    ></div>
  {/if}

  <!-- Sidebar -->
  <div
    class="h-full fixed top-0 left-0 z-50 transition-transform duration-300 ease md:relative"
    class:translate-x-0={isMobile && $sidebarOpen}
    class:translate-x-[-280px]={isMobile && !$sidebarOpen}
  >
    <Sidebar onNewSession={showNewSessionModal} />
  </div>

  <!-- Main -->
  <div class="flex-1 flex flex-col w-full min-w-0">
    <HeaderBar />
    <ChatArea />
  </div>

  <!-- New Session Modal -->
  <NewSessionModal />

  <!-- Toast Container -->
  <ToastContainer />

  <!-- tmux Session Picker -->
  <TmuxSessionPicker />

  <!-- tmux Terminal Modal -->
  <TmuxTerminalModal />

  <!-- tmux Window Picker -->
  <TmuxWindowPicker />

  <!-- Floating tmux Connect Button -->
  {#if $activeSession && activeSessionInfo}
    <button
      class="fixed bottom-6 right-6 z-40 w-12 h-12 rounded-full shadow-lg bg-ctp-green text-ctp-crust hover:bg-ctp-green/90 transition-all hover:scale-105 active:scale-95 flex items-center justify-center cursor-pointer group hover:shadow-xl hover:shadow-ctp-green/20"
      title="Connect to tmux session"
      onclick={openTmuxPicker}
    >
      <Terminal size={20} class="group-hover:scale-110 transition-transform" />
    </button>
  {/if}
</div>
