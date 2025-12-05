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
    throw new Error('Unauthorized');
  }

  if (!response.ok) {
    throw new Error(`HTTP ${response.status}: ${response.statusText}`);
  }

  return response;
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
}

export function clearCredentials() {
  localStorage.removeItem('autoscan_credentials');
}

export function hasCredentials() {
  return localStorage.getItem('autoscan_credentials') !== null;
}
