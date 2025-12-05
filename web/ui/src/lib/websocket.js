export function createLogConnection(onMessage, onStatusChange) {
  let ws = null;
  let reconnectTimeout = null;
  let isPaused = false;

  function connect() {
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const wsUrl = `${protocol}//${window.location.host}/api/logs`;

    ws = new WebSocket(wsUrl);

    ws.onopen = () => {
      onStatusChange('connected');
    };

    ws.onmessage = (event) => {
      if (!isPaused) {
        const line = event.data.trim();
        if (!line) return;

        // Parse console-formatted log: "Dec  5 23:02:16 INF message key=value ..."
        // The timestamp is in Go's time.Stamp format (Jan _2 15:04:05)
        const match = line.match(/^(\w{3}\s+\d+\s+\d{2}:\d{2}:\d{2})\s+(TRC|DBG|INF|WRN|ERR|FTL)\s+(.*)$/);

        if (match) {
          const [, timestamp, levelCode, message] = match;
          const levelMap = {
            'TRC': 'trace',
            'DBG': 'debug',
            'INF': 'info',
            'WRN': 'warn',
            'ERR': 'error',
            'FTL': 'fatal'
          };
          onMessage({
            time: timestamp,
            level: levelMap[levelCode] || 'info',
            message: message
          });
        } else {
          // Fallback for unrecognized format
          onMessage({
            message: line,
            level: 'info',
            time: new Date().toLocaleTimeString()
          });
        }
      }
    };

    ws.onclose = () => {
      onStatusChange('disconnected');
      // Auto-reconnect after 3 seconds
      reconnectTimeout = setTimeout(connect, 3000);
    };

    ws.onerror = () => {
      onStatusChange('error');
    };
  }

  function disconnect() {
    if (reconnectTimeout) {
      clearTimeout(reconnectTimeout);
      reconnectTimeout = null;
    }
    if (ws) {
      ws.close();
      ws = null;
    }
  }

  function pause() {
    isPaused = true;
  }

  function resume() {
    isPaused = false;
  }

  return {
    connect,
    disconnect,
    pause,
    resume,
    isPaused: () => isPaused
  };
}
