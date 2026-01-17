import { useState, useEffect, useCallback } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { createDb, type WispDB } from './db/store'
import { createActions } from './db/actions'
import { Login } from './components/Login'
import { Dashboard } from './components/Dashboard'
import { Session } from './components/Session'
import { Disconnected } from './components/Disconnected'

type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'error'

export function App() {
  const [token, setToken] = useState<string | null>(null)
  const [db, setDb] = useState<WispDB | null>(null)
  const [connectionState, setConnectionState] = useState<ConnectionState>('disconnected')
  const [error, setError] = useState<string | null>(null)
  const [reconnectKey, setReconnectKey] = useState(0)

  const handleReconnect = useCallback(() => {
    setConnectionState('disconnected')
    setDb(null)
    setError(null)
    setReconnectKey(k => k + 1)
  }, [])

  useEffect(() => {
    if (token) {
      setConnectionState('connecting')
      setError(null)
      let cancelled = false

      const database = createDb({
        token,
        onDisconnect: (err) => {
          if (!cancelled) {
            setError(err.message)
            setConnectionState('disconnected')
            setDb(null)
          }
        },
      })

      database.preload()
        .then(() => {
          if (cancelled) return
          setDb(database)
          setConnectionState('connected')
        })
        .catch((err: Error) => {
          if (cancelled) return
          setError(err.message)
          setConnectionState('error')
          setToken(null)
        })
      return () => {
        cancelled = true
        database.close()
      }
    } else {
      setDb(null)
      setConnectionState('disconnected')
    }
  }, [token, reconnectKey])

  const handleLogout = () => {
    setToken(null)
    setDb(null)
  }

  if (!token) {
    return <Login onLogin={setToken} />
  }

  if (connectionState === 'connecting') {
    return (
      <div className="loading">
        <p>Connecting...</p>
      </div>
    )
  }

  if (connectionState === 'error') {
    return (
      <div className="error">
        <p>Connection error: {error}</p>
        <button onClick={() => setToken(null)}>Try Again</button>
      </div>
    )
  }

  // Show disconnected page when connection is lost after being connected
  if (connectionState === 'disconnected' && token && !db) {
    return <Disconnected onReconnect={handleReconnect} error={error || undefined} />
  }

  if (!db) {
    return null
  }

  const actions = createActions(db, token)

  return (
    <BrowserRouter>
      <div className="app">
        <header className="app-header">
          <h1>Wisp</h1>
          <button onClick={handleLogout} className="logout-btn">Logout</button>
        </header>
        <main>
          <Routes>
            <Route path="/" element={<Dashboard db={db} />} />
            <Route path="/session/:id" element={<Session db={db} actions={actions} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </main>
      </div>
    </BrowserRouter>
  )
}
