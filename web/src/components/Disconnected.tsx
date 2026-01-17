interface DisconnectedProps {
  onReconnect: () => void
  error?: string
}

export function Disconnected({ onReconnect, error }: DisconnectedProps) {
  return (
    <div className="disconnected">
      <div className="disconnected-content">
        <div className="disconnected-icon">&#x26A0;</div>
        <h1>Disconnected</h1>
        <p className="disconnected-message">
          {error || 'The connection to the wisp server was lost.'}
        </p>
        <p className="disconnected-hint">
          The wisp session may have ended or the server may be unreachable.
        </p>
        <button onClick={onReconnect} className="reconnect-btn">
          Reconnect
        </button>
      </div>
    </div>
  )
}
