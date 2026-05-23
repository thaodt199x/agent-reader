<script>
  import { tmuxTerminalTarget } from '$lib/stores/tmux.svelte.js';
  import { X, AlertTriangle } from '@lucide/svelte';
  import { Terminal } from 'xterm';
  import { FitAddon } from 'xterm-addon-fit';
  import 'xterm/css/xterm.css';

  let terminalRef;

  let terminal = $state(null);
  let fitAddon = $state(null);
  let ws = $state(null);
  let status = $state('disconnected');
  let sessionName = $state('');
  let windowIndex = $state(null);
  let reconnectAttempt = $state(0);
  let reconnectTimer = $state(null);

  function computeBackoff() {
    const delay = Math.min(1000 * Math.pow(2, reconnectAttempt), 16000);
    reconnectAttempt++;
    return delay;
  }

  function disconnect() {
    if (ws) {
      ws.onclose = null;
      ws.close();
      ws = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    status = 'disconnected';
  }

  function closeTerminal() {
    disconnect();
    tmuxTerminalTarget.set(null);
    reconnectAttempt = 0;
    if (terminal) {
      terminal.dispose();
      terminal = null;
      fitAddon = null;
    }
  }

  function connect() {
    if (!sessionName) return;
    status = 'connecting';

    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    let url = `${proto}//${location.host}/ws/tmux/${encodeURIComponent(sessionName)}`;
    if (windowIndex !== null) {
      url += `?window=${windowIndex}`;
    }
    const socket = new WebSocket(url);

    socket.onopen = () => {
      status = 'connected';
      reconnectAttempt = 0;
      if (windowIndex === null && terminal) {
        socket.send(JSON.stringify({
          type: 'resize',
          cols: terminal.cols,
          rows: terminal.rows,
        }));
      }
    };

    socket.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data);
        if (msg.type === 'data' && terminal) {
          terminal.write('\x1b[2J\x1b[H' + msg.content);
        } else if (msg.type === 'session_end') {
          status = 'ended';
          socket.close();
        } else if (msg.type === 'error') {
          console.error('[tmux] server error:', msg.error);
        }
      } catch (e) {
        console.error('[tmux] ws parse error:', e);
      }
    };

    socket.onclose = () => {
      if (status === 'ended') return;
      status = 'disconnected';
      const delay = computeBackoff();
      reconnectTimer = setTimeout(() => {
        const current = tmuxTerminalTarget.get();
        if (current && current.session === sessionName) {
          connect();
        }
      }, delay);
    };

    socket.onerror = () => {
      socket.close();
    };

    ws = socket;
  }

  $effect(() => {
    const target = $tmuxTerminalTarget;
    if (target) {
      sessionName = target.session;
      windowIndex = target.window !== undefined ? target.window : null;
      reconnectAttempt = 0;
      disconnect(); // clean up previous connection before connecting to new target

      if (!terminal) {
        terminal = new Terminal({
          cursorBlink: true,
          fontSize: 13,
          fontFamily: '"JetBrains Mono", "Fira Code", "Cascadia Code", Menlo, monospace',
          theme: {
            background: '#1e1e2e',
            foreground: '#cdd6f4',
            cursor: '#f5e0dc',
            selectionBackground: '#585b7066',
            black: '#45475a',
            red: '#f38ba8',
            green: '#a6e3a1',
            yellow: '#f9e2af',
            blue: '#89b4fa',
            magenta: '#f5c2e7',
            cyan: '#94e2d5',
            white: '#bac2de',
            brightBlack: '#585b70',
            brightRed: '#f38ba8',
            brightGreen: '#a6e3a1',
            brightYellow: '#f9e2af',
            brightBlue: '#89b4fa',
            brightMagenta: '#f5c2e7',
            brightCyan: '#94e2d5',
            brightWhite: '#a6adc8',
          },
        });

        fitAddon = new FitAddon();
        terminal.loadAddon(fitAddon);
        terminal.open(terminalRef);

        terminal.onData((data) => {
          if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'data', content: data }));
          }
        });

        if (windowIndex === null) {
          terminal.onResize(({ cols, rows }) => {
            if (ws && ws.readyState === WebSocket.OPEN) {
              ws.send(JSON.stringify({ type: 'resize', cols, rows }));
            }
          });

          requestAnimationFrame(() => {
            if (fitAddon) fitAddon.fit();
          });
        }
      }

      connect();

      return () => {
        disconnect();
        if (terminal) {
          terminal.dispose();
          terminal = null;
          fitAddon = null;
        }
      };
    } else {
      closeTerminal();
    }
  });

  $effect(() => {
    if (windowIndex !== null) return;
    if (!fitAddon || !terminal) return;
    const observer = new ResizeObserver(() => {
      fitAddon.fit();
    });
    if (terminalRef) observer.observe(terminalRef);
    return () => observer.disconnect();
  });
</script>

{#if $tmuxTerminalTarget}
  <div class="fixed inset-0 z-50 flex items-center justify-center">
    <div class="absolute inset-0 bg-black/70 backdrop-blur-sm" onclick={() => {}}></div>
    <div class="relative bg-ctp-mantle border border-ctp-surface0 rounded-2xl shadow-2xl w-[90vw] h-[80vh] max-w-[1200px] animate-fadeIn overflow-hidden flex flex-col">
      <!-- Header -->
      <div class="px-4 py-3 border-b border-ctp-surface0 flex items-center justify-between bg-ctp-crust">
        <div class="flex items-center gap-3">
          <span class="text-sm font-semibold text-ctp-text font-mono">
            {sessionName}{windowIndex !== null ? ':' + windowIndex : ''}
          </span>
          <span class="w-[8px] h-[8px] rounded-full flex-shrink-0 {
            status === 'connected' ? 'bg-ctp-green' :
            status === 'connecting' ? 'bg-ctp-yellow animate-pulse' :
            status === 'ended' ? 'bg-ctp-red' :
            'bg-ctp-red'
          }" style="{status === 'connecting' ? 'animation-duration: 1s' : ''}"></span>
          <span class="text-[11px] text-ctp-overlay0 capitalize">{status}</span>
        </div>
        <div class="flex items-center gap-2">
          {#if status === 'disconnected'}
            <button
              class="px-2 py-1 rounded text-[11px] font-medium bg-ctp-blue/20 text-ctp-blue hover:bg-ctp-blue/30 transition-colors cursor-pointer"
              onclick={() => { reconnectAttempt = 0; connect(); }}
            >
              Reconnect
            </button>
          {/if}
          <button
            class="text-ctp-overlay0 hover:text-ctp-text transition-colors p-1 rounded-md hover:bg-ctp-surface0 flex items-center justify-center cursor-pointer"
            onclick={closeTerminal}
          >
            <X class="h-4 w-4" />
          </button>
        </div>
      </div>

      <!-- Terminal area -->
      <div class="flex-1 relative bg-ctp-crust">
        <div bind:this={terminalRef} class="absolute inset-0"></div>

        {#if status === 'ended'}
          <div class="absolute inset-0 bg-black/60 flex items-center justify-center">
            <div class="flex items-center gap-3 px-4 py-3 rounded-lg bg-ctp-mantle border border-ctp-surface0">
              <AlertTriangle size={16} class="text-ctp-red" />
              <span class="text-sm text-ctp-text">Session ended</span>
              <button
                class="ml-2 px-3 py-1 rounded text-xs font-medium bg-ctp-surface0 text-ctp-overlay0 hover:text-ctp-text transition-colors cursor-pointer"
                onclick={closeTerminal}
              >
                Close
              </button>
            </div>
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}
