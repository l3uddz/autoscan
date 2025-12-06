import { writable, derived } from 'svelte/store';

// Auth state: null = checking, true = authenticated, false = needs login
export const isAuthenticated = writable(null);
export const authRequired = writable(true);
export const authError = writable(null);

// Scans store
export const scans = writable([]);
export const scansLoading = writable(false);
export const scansError = writable(null);

// Derived store for scan count
export const scanCount = derived(scans, $scans => $scans.length);

// Logs store
export const logs = writable([]);
export const maxLogs = 500;

// Log connection status
export const logConnectionStatus = writable('disconnected');

// UI state
export const activeTab = writable('scans');

// Config store (for triggers/targets list)
export const config = writable({
  triggers: [],
  targets: []
});
