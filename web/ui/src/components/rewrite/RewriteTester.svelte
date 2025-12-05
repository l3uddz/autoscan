<script>
  import { onMount } from 'svelte';
  import { config } from '../../lib/stores.js';
  import { getConfig, testRewrite } from '../../lib/api.js';

  let testPath = '';
  let selectedTrigger = '';
  let selectedTarget = '';
  let loading = false;
  let result = null;
  let error = null;

  onMount(async () => {
    try {
      const cfg = await getConfig();
      config.set(cfg);
    } catch (err) {
      error = `Failed to load config: ${err.message}`;
    }
  });

  async function handleTest() {
    if (!testPath.trim()) return;

    loading = true;
    error = null;
    result = null;

    try {
      const [triggerKind, triggerName] = selectedTrigger ? selectedTrigger.split(':') : ['', ''];
      const [targetKind, targetName] = selectedTarget ? selectedTarget.split(':') : ['', ''];

      result = await testRewrite(
        testPath.trim(),
        triggerKind,
        triggerName,
        targetKind,
        targetName
      );
    } catch (err) {
      error = err.message;
    } finally {
      loading = false;
    }
  }

  $: triggerOptions = ($config.triggers || []).map(t => ({
    value: `${t.type}:${t.name}`,
    label: `${t.type} - ${t.name}`
  }));

  $: targetOptions = ($config.targets || []).map(t => ({
    value: `${t.type}:${t.name}`,
    label: `${t.type} - ${t.name}`
  }));
</script>

<div class="rewrite-tester">
  <h2>Rewrite Tester</h2>
  <p class="description">
    Test how paths are transformed through trigger and target rewrite rules.
  </p>

  <div class="card">
    <div class="card-body">
      <div class="form">
        <div class="form-group">
          <label class="label" for="test-path">Test Path</label>
          <input
            id="test-path"
            type="text"
            class="input"
            placeholder="/mnt/remote/Media/Movies/Movie Name (2023)"
            bind:value={testPath}
          />
        </div>

        <div class="selectors">
          <div class="form-group">
            <label class="label" for="trigger">Trigger</label>
            <select id="trigger" class="select" bind:value={selectedTrigger}>
              <option value="">None (no trigger rewrite)</option>
              {#each triggerOptions as opt}
                <option value={opt.value}>{opt.label}</option>
              {/each}
            </select>
          </div>

          <div class="form-group">
            <label class="label" for="target">Target</label>
            <select id="target" class="select" bind:value={selectedTarget}>
              <option value="">None (no target rewrite)</option>
              {#each targetOptions as opt}
                <option value={opt.value}>{opt.label}</option>
              {/each}
            </select>
          </div>
        </div>

        <button class="btn btn-primary" onclick={handleTest} disabled={loading || !testPath.trim()}>
          {loading ? 'Testing...' : 'Test Rewrite'}
        </button>
      </div>
    </div>
  </div>

  {#if error}
    <div class="error-box">{error}</div>
  {/if}

  {#if result}
    <div class="card">
      <div class="card-body">
        <h3>Transformation Result</h3>

        <div class="chain">
          <div class="step">
            <div class="step-label">Original Path</div>
            <div class="step-path">{result.original}</div>
          </div>

          <div class="arrow" class:matched={result.trigger_matched}>
            <div class="arrow-line"></div>
            <span class="arrow-label">
              {result.trigger_matched ? 'Trigger Rewrite Applied' : 'No Trigger Match'}
            </span>
          </div>

          <div class="step" class:changed={result.original !== result.after_trigger}>
            <div class="step-label">After Trigger Rewrite</div>
            <div class="step-path">{result.after_trigger}</div>
          </div>

          <div class="arrow" class:matched={result.target_matched}>
            <div class="arrow-line"></div>
            <span class="arrow-label">
              {result.target_matched ? 'Target Rewrite Applied' : 'No Target Match'}
            </span>
          </div>

          <div class="step final" class:changed={result.after_trigger !== result.after_target}>
            <div class="step-label">Final Path (to Target)</div>
            <div class="step-path">{result.after_target}</div>
          </div>
        </div>

        {#if result.original === result.after_target}
          <div class="note">No transformations were applied to this path.</div>
        {/if}

        {#if result.trigger_rules?.length > 0}
          <div class="rules">
            <h4>Trigger Rules</h4>
            {#each result.trigger_rules as rule}
              <div class="rule">
                <code>from:</code> <span class="rule-value">{rule.from}</span><br/>
                <code>to:</code> <span class="rule-value">{rule.to}</span>
              </div>
            {/each}
          </div>
        {/if}

        {#if result.target_rules?.length > 0}
          <div class="rules">
            <h4>Target Rules</h4>
            {#each result.target_rules as rule}
              <div class="rule">
                <code>from:</code> <span class="rule-value">{rule.from}</span><br/>
                <code>to:</code> <span class="rule-value">{rule.to}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .rewrite-tester {
    padding: 1.5rem;
  }

  .description {
    color: var(--text-secondary);
    margin-bottom: 1rem;
  }

  .card-body {
    padding: 1rem;
  }

  .card-body h3 {
    margin-bottom: 1.5rem;
  }

  .form {
    display: flex;
    flex-direction: column;
    gap: 1rem;
  }

  .form-group {
    display: flex;
    flex-direction: column;
  }

  .selectors {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 1rem;
  }

  .error-box {
    color: var(--error-color);
    padding: 1rem;
    background: var(--error-bg);
    border-radius: 4px;
    margin-top: 1rem;
  }

  .chain {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
  }

  .step {
    background: var(--bg-tertiary);
    padding: 1rem;
    border-radius: 4px;
    border-left: 4px solid var(--border-color);
  }

  .step.changed {
    border-left-color: var(--accent-color);
  }

  .step.final {
    border-left-color: var(--success-color);
  }

  .step-label {
    font-size: 0.75rem;
    color: var(--text-secondary);
    text-transform: uppercase;
    margin-bottom: 0.5rem;
  }

  .step-path {
    font-family: monospace;
    word-break: break-all;
    font-size: 0.875rem;
  }

  .arrow {
    display: flex;
    flex-direction: column;
    align-items: center;
    padding: 0.5rem 0;
    color: var(--text-muted);
  }

  .arrow.matched {
    color: var(--accent-color);
  }

  .arrow-line {
    width: 2px;
    height: 20px;
    background: currentColor;
    position: relative;
  }

  .arrow-line::after {
    content: '';
    position: absolute;
    bottom: -6px;
    left: -5px;
    width: 0;
    height: 0;
    border-left: 6px solid transparent;
    border-right: 6px solid transparent;
    border-top: 6px solid currentColor;
  }

  .arrow-label {
    font-size: 0.75rem;
    margin-top: 0.5rem;
  }

  .note {
    margin-top: 1rem;
    color: var(--text-secondary);
    font-style: italic;
    text-align: center;
  }

  .rules {
    margin-top: 1.5rem;
    padding-top: 1rem;
    border-top: 1px solid var(--border-color);
  }

  .rules h4 {
    font-size: 0.875rem;
    margin-bottom: 0.5rem;
    color: var(--text-secondary);
  }

  .rule {
    background: var(--bg-tertiary);
    padding: 0.5rem;
    border-radius: 4px;
    font-size: 0.75rem;
    margin-bottom: 0.5rem;
  }

  .rule code {
    color: var(--accent-color);
  }

  .rule-value {
    font-family: monospace;
    color: var(--text-primary);
  }
</style>
