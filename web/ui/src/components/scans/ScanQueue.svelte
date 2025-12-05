<script>
  import { onMount, onDestroy } from 'svelte';
  import { scans, scansLoading, scansError } from '../../lib/stores.js';
  import { getScans, addScan } from '../../lib/api.js';
  import { formatRelativeTime } from '../../lib/utils.js';

  let refreshInterval;
  let autoRefresh = true;
  const REFRESH_INTERVAL = 5000;

  // Add scan form
  let newFolder = '';
  let newPriority = 0;
  let addLoading = false;
  let addMessage = null;
  let addMessageType = 'success';

  async function loadScans() {
    scansLoading.set(true);
    scansError.set(null);
    try {
      const data = await getScans();
      scans.set(data.scans || []);
    } catch (err) {
      scansError.set(err.message);
    } finally {
      scansLoading.set(false);
    }
  }

  function toggleAutoRefresh() {
    autoRefresh = !autoRefresh;
    if (autoRefresh) {
      startAutoRefresh();
    } else {
      stopAutoRefresh();
    }
  }

  function startAutoRefresh() {
    refreshInterval = setInterval(loadScans, REFRESH_INTERVAL);
  }

  function stopAutoRefresh() {
    if (refreshInterval) {
      clearInterval(refreshInterval);
    }
  }

  async function handleAddScan(e) {
    e.preventDefault();
    if (!newFolder.trim()) return;

    addLoading = true;
    addMessage = null;

    try {
      await addScan(newFolder.trim(), newPriority);
      addMessage = 'Scan added to queue';
      addMessageType = 'success';
      newFolder = '';
      newPriority = 0;
      loadScans();
    } catch (err) {
      addMessage = `Failed: ${err.message}`;
      addMessageType = 'error';
    } finally {
      addLoading = false;
      setTimeout(() => { addMessage = null; }, 3000);
    }
  }

  onMount(() => {
    loadScans();
    if (autoRefresh) {
      startAutoRefresh();
    }
  });

  onDestroy(() => {
    stopAutoRefresh();
  });
</script>

<div class="scan-queue">
  <div class="header">
    <h2>Scan Queue</h2>
    <div class="controls">
      <span class="count">{$scans.length} scans queued</span>
      <button class="btn btn-secondary" onclick={loadScans} disabled={$scansLoading}>
        {$scansLoading ? 'Refreshing...' : 'Refresh'}
      </button>
      <label class="auto-refresh">
        <input type="checkbox" bind:checked={autoRefresh} onchange={toggleAutoRefresh} />
        Auto-refresh
      </label>
    </div>
  </div>

  <div class="card">
    <div class="card-body">
      <h3>Add Scan</h3>
      <form class="add-form" onsubmit={handleAddScan}>
        <div class="form-group">
          <label class="label" for="folder">Folder Path</label>
          <input
            id="folder"
            type="text"
            class="input"
            placeholder="/path/to/media/folder"
            bind:value={newFolder}
            required
          />
        </div>
        <div class="form-row">
          <div class="form-group priority-group">
            <label class="label" for="priority">Priority</label>
            <input
              id="priority"
              type="number"
              class="input"
              bind:value={newPriority}
              min="0"
            />
          </div>
          <button class="btn btn-primary" type="submit" disabled={addLoading || !newFolder.trim()}>
            {addLoading ? 'Adding...' : 'Add Scan'}
          </button>
        </div>
        {#if addMessage}
          <div class="message {addMessageType}">{addMessage}</div>
        {/if}
      </form>
    </div>
  </div>

  {#if $scansError}
    <div class="error-box">{$scansError}</div>
  {/if}

  <div class="card">
    <div class="scan-list">
      {#if $scans.length === 0}
        <p class="empty">No scans in queue</p>
      {:else}
        {#each $scans as scan (scan.folder)}
          <div class="scan-item">
            <div class="folder">{scan.folder}</div>
            <div class="meta">
              <span class="badge" class:high={scan.priority > 0}>
                Priority: {scan.priority}
              </span>
              <span class="time" title={new Date(scan.time).toLocaleString()}>
                {formatRelativeTime(scan.time)}
              </span>
            </div>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>

<style>
  .scan-queue {
    padding: 1.5rem;
  }

  .header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
  }

  .controls {
    display: flex;
    gap: 1rem;
    align-items: center;
  }

  .count {
    color: var(--text-secondary);
    font-size: 0.875rem;
  }

  .auto-refresh {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--text-secondary);
    font-size: 0.875rem;
    cursor: pointer;
  }

  .card-body {
    padding: 1rem;
  }

  .card-body h3 {
    margin-bottom: 1rem;
    font-size: 1rem;
  }

  .add-form {
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
  }

  .form-group {
    display: flex;
    flex-direction: column;
  }

  .form-row {
    display: flex;
    gap: 1rem;
    align-items: flex-end;
  }

  .priority-group {
    width: 100px;
  }

  .message {
    padding: 0.5rem;
    border-radius: 4px;
    text-align: center;
    font-size: 0.875rem;
  }

  .message.success {
    background: var(--success-bg);
    color: var(--success-color);
  }

  .message.error {
    background: var(--error-bg);
    color: var(--error-color);
  }

  .error-box {
    color: var(--error-color);
    padding: 1rem;
    background: var(--error-bg);
    border-radius: 4px;
    margin-bottom: 1rem;
  }

  .scan-list {
    max-height: 50vh;
    overflow-y: auto;
  }

  .scan-item {
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--border-color);
  }

  .scan-item:last-child {
    border-bottom: none;
  }

  .folder {
    font-family: monospace;
    font-size: 0.875rem;
    word-break: break-all;
    margin-bottom: 0.5rem;
  }

  .meta {
    display: flex;
    gap: 1rem;
    align-items: center;
    font-size: 0.75rem;
  }

  .meta .badge {
    background: var(--badge-bg);
  }

  .meta .badge.high {
    background: var(--accent-color);
    color: white;
  }

  .time {
    color: var(--text-muted);
  }

  .empty {
    color: var(--text-secondary);
    text-align: center;
    padding: 2rem;
  }
</style>
