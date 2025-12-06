import { isAuthenticated, authRequired, authError } from './stores.js';

const API_BASE = '/api';

function getAuthHeaders() {
  const credentials = localStorage.getItem('autoscan_credentials');
  if (credentials) {
    return { 'Authorization': `Basic ${credentials}` };
  }
  return {};
}

async function request(endpoint, options = {}) {
  const response = await fetch(`${API_BASE}${endpoint}`, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...getAuthHeaders(),
      ...options.headers
    }
  });

  if (response.status === 401) {
    isAuthenticated.set(false);
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`HTTP ${response.status}: ${response.statusText}`);
  }

  isAuthenticated.set(true);
  return response;
}

export async function getAuthStatus() {
  try {
    const response = await fetch(`${API_BASE}/auth/status`);
    const data = await response.json();
    return data.auth_required;
  } catch {
    // If we can't reach the endpoint, assume auth is required for safety
    return true;
  }
}

export async function checkAuth() {
  // First check if auth is required by the server
  const isAuthRequired = await getAuthStatus();
  authRequired.set(isAuthRequired);

  if (!isAuthRequired) {
    // No auth configured on server - allow access
    isAuthenticated.set(true);
    return true;
  }

  // Auth is required - check if we have credentials stored
  if (!hasCredentials()) {
    isAuthenticated.set(false);
    return false;
  }

  // Verify stored credentials are valid
  try {
    await request('/config');
    isAuthenticated.set(true);
    authError.set(null);
    return true;
  } catch (err) {
    if (err.message === 'Unauthorized') {
      isAuthenticated.set(false);
      return false;
    }
    // Network error or server issue - assume auth is OK if we have credentials
    isAuthenticated.set(true);
    return true;
  }
}

export async function getScans() {
  const response = await request('/scans');
  return response.json();
}

export async function addScan(folder, priority = 0) {
  return request('/scans', {
    method: 'POST',
    body: JSON.stringify({ folder, priority })
  });
}

export async function getConfig() {
  const response = await request('/config');
  return response.json();
}

export async function testRewrite(path, triggerKind, triggerName, targetKind, targetName) {
  const response = await request('/rewrite', {
    method: 'POST',
    body: JSON.stringify({
      path,
      trigger_kind: triggerKind,
      trigger_name: triggerName,
      target_kind: targetKind,
      target_name: targetName
    })
  });
  return response.json();
}

export function setCredentials(username, password) {
  const encoded = btoa(`${username}:${password}`);
  localStorage.setItem('autoscan_credentials', encoded);
  authError.set(null);
}

export function clearCredentials() {
  localStorage.removeItem('autoscan_credentials');
  isAuthenticated.set(false);
}

export function hasCredentials() {
  return localStorage.getItem('autoscan_credentials') !== null;
}
