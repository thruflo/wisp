import { useState, useEffect } from 'react'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { createDb, type WispDB } from './db/store'
import { createActions } from './db/actions'
import { Login } from './components/Login'
import { Dashboard } from './components/Dashboard'
import { Session } from './components/Session'

export function App() {
  const [token, setToken] = useState<string | null>(null)
  const [db, setDb] = useState<WispDB | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (token) {
      setLoading(true)
      setError(null)
      const database = createDb(token)
      database.preload()
        .then(() => {
          setDb(database)
          setLoading(false)
        })
        .catch((err: Error) => {
          setError(err.message)
          setLoading(false)
          setToken(null)
        })
      return () => database.close()
    } else {
      setDb(null)
    }
  }, [token])

  const handleLogout = () => {
    setToken(null)
    setDb(null)
  }

  if (!token) {
    return <Login onLogin={setToken} />
  }

  if (loading) {
    return (
      <div className="loading">
        <p>Connecting...</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="error">
        <p>Connection error: {error}</p>
        <button onClick={() => setToken(null)}>Try Again</button>
      </div>
    )
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
