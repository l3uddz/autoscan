<script>
  import { onMount, onDestroy, tick } from 'svelte';
  import { logs, logConnectionStatus, maxLogs } from '../../lib/stores.js';
  import { createLogConnection } from '../../lib/websocket.js';

  let logContainer;
  let connection;
  let isPaused = false;
  let autoScroll = true;

  function handleLogMessage(entry) {
    logs.update(currentLogs => {
      const newLogs = [...currentLogs, entry];
      if (newLogs.length > maxLogs) {
        return newLogs.slice(-maxLogs);
      }
      return newLogs;
    });

    if (autoScroll) {
      tick().then(() => {
        if (logContainer) {
          logContainer.scrollTop = logContainer.scrollHeight;
        }
      });
    }
  }

  function handleStatusChange(status) {
    logConnectionStatus.set(status);
  }

  function togglePause() {
    isPaused = !isPaused;
    if (isPaused) {
      connection.pause();
    } else {
      connection.resume();
    }
  }

  function clearLogs() {
    logs.set([]);
  }

  function handleScroll() {
    if (!logContainer) return;
    const { scrollTop, scrollHeight, clientHeight } = logContainer;
    autoScroll = scrollHeight - scrollTop - clientHeight < 50;
  }

  function getLevelClass(level) {
    const lvl = (level || 'info').toLowerCase();
    return {
      trace: 'trace',
      debug: 'debug',
      info: 'info',
      warn: 'warn',
      warning: 'warn',
      error: 'error',
      fatal: 'error'
    }[lvl] || 'info';
  }

  function getStatusClass(status) {
    return {
      connected: 'badge-success',
      disconnected: 'badge-warning',
      error: 'badge-error'
    }[status] || '';
  }

  onMount(() => {
    connection = createLogConnection(handleLogMessage, handleStatusChange);
    connection.connect();
  });

  onDestroy(() => {
    if (connection) {
      connection.disconnect();
    }
  });
</script>

<div class="log-panel">
  <div class="header">
    <h2>Live Logs</h2>
    <div class="controls">
      <span class="badge {getStatusClass($logConnectionStatus)}">
        {$logConnectionStatus}
      </span>
      <button
        class="btn {isPaused ? 'btn-primary' : 'btn-secondary'}"
        onclick={togglePause}
      >
        {isPaused ? 'Resume' : 'Pause'}
      </button>
      <button class="btn btn-secondary" onclick={clearLogs}>
        Clear
      </button>
      <label class="auto-scroll">
        <input type="checkbox" bind:checked={autoScroll} />
        Auto-scroll
      </label>
    </div>
  </div>

  <div
    class="log-container"
    bind:this={logContainer}
    onscroll={handleScroll}
  >
    {#each $logs as entry, i (i)}
      <div class="log-entry {getLevelClass(entry.level)}">
        <span class="time">{entry.time}</span>
        <span class="level">{(entry.level || 'INFO').toUpperCase()}</span>
        <span class="message">{entry.message}</span>
      </div>
    {/each}

    {#if $logs.length === 0}
      <p class="empty">No logs yet. Waiting for log messages...</p>
    {/if}
  </div>
</div>

<style>
  .log-panel {
    padding: 1.5rem;
    display: flex;
    flex-direction: column;
    height: 100%;
  }

  .header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
    gap: 1rem;
  }

  .controls {
    display: flex;
    gap: 1rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .auto-scroll {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.875rem;
    cursor: pointer;
  }

  .log-container {
    flex: 1;
    background: var(--bg-tertiary);
    border-radius: 4px;
    padding: 0.5rem;
    overflow-y: auto;
    font-family: monospace;
    font-size: 0.8125rem;
    min-height: 300px;
    max-height: calc(100vh - 150px);
  }

  .log-entry {
    display: flex;
    gap: 0.75rem;
    padding: 0.25rem 0.5rem;
    border-radius: 2px;
  }

  .log-entry:hover {
    background: var(--bg-hover);
  }

  .time {
    color: var(--text-muted);
    flex-shrink: 0;
  }

  .level {
    width: 45px;
    flex-shrink: 0;
    font-weight: bold;
  }

  .message {
    word-break: break-word;
  }

  .trace .level { color: var(--log-trace); }
  .debug .level { color: var(--log-debug); }
  .info .level { color: var(--log-info); }
  .warn .level { color: var(--log-warn); }
  .error .level { color: var(--log-error); }

  .error {
    background: var(--log-error-bg);
  }

  .warn {
    background: var(--log-warn-bg);
  }

  .empty {
    color: var(--text-muted);
    text-align: center;
    padding: 2rem;
  }

  @media (max-width: 768px) {
    .log-panel {
      padding: 1rem;
    }

    .header {
      flex-direction: column;
      align-items: flex-start;
    }

    .header h2 {
      font-size: 1.25rem;
    }

    .controls {
      width: 100%;
      justify-content: flex-start;
    }

    .log-container {
      min-height: 200px;
      max-height: calc(100vh - 200px);
      font-size: 0.75rem;
    }

    .log-entry {
      flex-wrap: wrap;
      gap: 0.25rem;
    }

    .time {
      font-size: 0.7rem;
    }

    .level {
      width: auto;
    }

    .message {
      width: 100%;
      margin-top: 0.25rem;
    }
  }
</style>
