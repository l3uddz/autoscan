<script>
  import { onMount } from 'svelte';
  import { activeTab, scanCount, isAuthenticated, authRequired } from './lib/stores.js';
  import { checkAuth, clearCredentials } from './lib/api.js';
  import ScanQueue from './components/scans/ScanQueue.svelte';
  import RewriteTester from './components/rewrite/RewriteTester.svelte';
  import LogPanel from './components/logs/LogPanel.svelte';
  import LoginForm from './components/auth/LoginForm.svelte';

  let mobileMenuOpen = false;

  const tabs = [
    { id: 'scans', label: 'Scan Queue' },
    { id: 'rewrite', label: 'Rewrite Tester' },
    { id: 'logs', label: 'Live Logs' }
  ];

  function selectTab(tabId) {
    activeTab.set(tabId);
    mobileMenuOpen = false;
  }

  function toggleMobileMenu() {
    mobileMenuOpen = !mobileMenuOpen;
  }

  function handleLogout() {
    clearCredentials();
    mobileMenuOpen = false;
  }

  onMount(() => {
    checkAuth();
  });
</script>

{#if $isAuthenticated === null}
  <div class="loading-screen">
    <div class="loading-content">
      <h1>autoscan</h1>
      <p>Loading...</p>
    </div>
  </div>
{:else if $isAuthenticated === false}
  <LoginForm />
{:else}
  <div class="layout">
    <button class="hamburger" onclick={toggleMobileMenu} aria-label="Toggle menu">
      <span class="hamburger-line"></span>
      <span class="hamburger-line"></span>
      <span class="hamburger-line"></span>
    </button>

    {#if mobileMenuOpen}
      <button
        class="overlay"
        onclick={() => mobileMenuOpen = false}
        aria-label="Close menu"
      ></button>
    {/if}

    <nav class="sidebar" class:open={mobileMenuOpen}>
      <div class="sidebar-header">
        <div class="logo">
          <h1>autoscan</h1>
        </div>
        <button class="close-btn" onclick={() => mobileMenuOpen = false} aria-label="Close menu">
          &times;
        </button>
      </div>

      <ul class="nav-tabs">
        {#each tabs as tab}
          <li>
            <button
              class:active={$activeTab === tab.id}
              onclick={() => selectTab(tab.id)}
            >
              {tab.label}
              {#if tab.id === 'scans' && $scanCount > 0}
                <span class="badge">{$scanCount}</span>
              {/if}
            </button>
          </li>
        {/each}
      </ul>

      {#if $authRequired}
        <div class="sidebar-footer">
          <button class="btn btn-secondary logout-btn" onclick={handleLogout}>
            Logout
          </button>
        </div>
      {/if}
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
{/if}

<style>
  .loading-screen {
    display: flex;
    align-items: center;
    justify-content: center;
    height: 100%;
    background: var(--bg-primary);
  }

  .loading-content {
    text-align: center;
  }

  .loading-content h1 {
    color: var(--accent-color);
    font-size: 1.75rem;
    margin-bottom: 1rem;
  }

  .loading-content p {
    color: var(--text-secondary);
  }

  .layout {
    display: flex;
    height: 100%;
  }

  .hamburger {
    display: none;
    position: fixed;
    top: 1rem;
    left: 1rem;
    z-index: 50;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 4px;
    padding: 0.5rem;
    cursor: pointer;
    flex-direction: column;
    gap: 4px;
  }

  .hamburger-line {
    display: block;
    width: 20px;
    height: 2px;
    background: var(--text-primary);
    border-radius: 1px;
  }

  .overlay {
    display: none;
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    z-index: 90;
    border: none;
    cursor: pointer;
  }

  .sidebar {
    display: flex;
    flex-direction: column;
    background: var(--bg-secondary);
    padding: 1rem;
    border-right: 1px solid var(--border-color);
    min-width: 200px;
  }

  .sidebar-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-bottom: 2rem;
  }

  .close-btn {
    display: none;
    background: none;
    border: none;
    color: var(--text-secondary);
    font-size: 1.5rem;
    cursor: pointer;
    padding: 0.25rem 0.5rem;
  }

  .close-btn:hover {
    color: var(--text-primary);
  }

  .logo h1 {
    font-size: 1.5rem;
    margin: 0;
    color: var(--accent-color);
  }

  .nav-tabs {
    list-style: none;
    padding: 0;
    margin: 0;
    flex: 1;
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

  .sidebar-footer {
    margin-top: auto;
    padding-top: 1rem;
    border-top: 1px solid var(--border-color);
  }

  .logout-btn {
    width: 100%;
  }

  .content {
    flex: 1;
    overflow: auto;
  }

  @media (max-width: 768px) {
    .hamburger {
      display: flex;
    }

    .overlay {
      display: block;
    }

    .sidebar {
      display: none;
      position: fixed;
      top: 0;
      left: 0;
      bottom: 0;
      z-index: 100;
      width: 280px;
      max-width: 85vw;
    }

    .sidebar.open {
      display: flex;
    }

    .close-btn {
      display: block;
    }

    .content {
      padding-top: 3.5rem;
    }
  }
</style>
