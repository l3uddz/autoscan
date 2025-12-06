<script>
  import { authError } from '../../lib/stores.js';
  import { setCredentials, checkAuth } from '../../lib/api.js';

  let username = '';
  let password = '';
  let loading = false;

  async function handleSubmit(e) {
    e.preventDefault();
    if (!username.trim() || !password.trim()) return;

    loading = true;
    authError.set(null);

    try {
      setCredentials(username.trim(), password);
      const success = await checkAuth();
      if (!success) {
        authError.set('Invalid username or password');
        password = '';
      }
    } catch (err) {
      authError.set('Connection error. Please try again.');
    } finally {
      loading = false;
    }
  }
</script>

<div class="login-container">
  <div class="login-card">
    <div class="logo">
      <h1>autoscan</h1>
    </div>

    <form class="login-form" onsubmit={handleSubmit}>
      <div class="form-group">
        <label class="label" for="username">Username</label>
        <input
          id="username"
          type="text"
          class="input"
          bind:value={username}
          placeholder="Enter username"
          autocomplete="username"
          required
        />
      </div>

      <div class="form-group">
        <label class="label" for="password">Password</label>
        <input
          id="password"
          type="password"
          class="input"
          bind:value={password}
          placeholder="Enter password"
          autocomplete="current-password"
          required
        />
      </div>

      {#if $authError}
        <div class="error-message">{$authError}</div>
      {/if}

      <button
        type="submit"
        class="btn btn-primary login-btn"
        disabled={loading || !username.trim() || !password.trim()}
      >
        {loading ? 'Signing in...' : 'Sign In'}
      </button>
    </form>
  </div>
</div>

<style>
  .login-container {
    display: flex;
    align-items: center;
    justify-content: center;
    min-height: 100%;
    padding: 1rem;
    background: var(--bg-primary);
  }

  .login-card {
    width: 100%;
    max-width: 360px;
    background: var(--bg-secondary);
    border: 1px solid var(--border-color);
    border-radius: 8px;
    padding: 2rem;
  }

  .logo {
    text-align: center;
    margin-bottom: 2rem;
  }

  .logo h1 {
    font-size: 1.75rem;
    color: var(--accent-color);
    margin: 0;
  }

  .login-form {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .form-group {
    display: flex;
    flex-direction: column;
  }

  .error-message {
    color: var(--error-color);
    background: var(--error-bg);
    padding: 0.75rem;
    border-radius: 4px;
    font-size: 0.875rem;
    text-align: center;
  }

  .login-btn {
    width: 100%;
    padding: 0.75rem;
    margin-top: 0.5rem;
  }
</style>
