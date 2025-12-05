<script>
  import { activeTab, scanCount } from './lib/stores.js';
  import ScanQueue from './components/scans/ScanQueue.svelte';
  import RewriteTester from './components/rewrite/RewriteTester.svelte';
  import LogPanel from './components/logs/LogPanel.svelte';

  const tabs = [
    { id: 'scans', label: 'Scan Queue' },
    { id: 'rewrite', label: 'Rewrite Tester' },
    { id: 'logs', label: 'Live Logs' }
  ];
</script>

<div class="layout">
  <nav class="sidebar">
    <div class="logo">
      <h1>autoscan</h1>
    </div>

    <ul class="nav-tabs">
      {#each tabs as tab}
        <li>
          <button
            class:active={$activeTab === tab.id}
            onclick={() => activeTab.set(tab.id)}
          >
            {tab.label}
            {#if tab.id === 'scans' && $scanCount > 0}
              <span class="badge">{$scanCount}</span>
            {/if}
          </button>
        </li>
      {/each}
    </ul>
  </nav>

  <main class="content">
    {#if $activeTab === 'scans'}
      <ScanQueue />
    {:else if $activeTab === 'rewrite'}
      <RewriteTester />
    {:else if $activeTab === 'logs'}
      <LogPanel />
    {/if}
  </main>
</div>

<style>
  .layout {
    display: flex;
    height: 100%;
  }

  .sidebar {
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
    padding: 1rem;
    border-right: 1px solid var(--border-color);
    min-width: 200px;
  }

  .logo h1 {
    font-size: 1.5rem;
    margin-bottom: 2rem;
    color: var(--accent-color);
  }

  .nav-tabs {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .nav-tabs li {
    margin-bottom: 0.5rem;
  }

  .nav-tabs button {
    width: 100%;
    padding: 0.75rem 1rem;
    border: none;
    background: transparent;
    color: var(--text-secondary);
    cursor: pointer;
    text-align: left;
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 0.875rem;
  }

  .nav-tabs button:hover {
    background: var(--bg-hover);
  }

  .nav-tabs button.active {
    background: var(--accent-color);
    color: white;
  }

  .nav-tabs .badge {
    background: rgba(255, 255, 255, 0.2);
    color: inherit;
  }

  .content {
    flex: 1;
    overflow: auto;
  }
</style>
