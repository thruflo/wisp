import { useState, type FormEvent } from 'react'

interface LoginProps {
  onLogin: (token: string) => void
}

export function Login({ onLogin }: LoginProps) {
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setLoading(true)
    setError(null)

    try {
      const res = await fetch('/auth', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ password }),
      })

      if (!res.ok) {
        if (res.status === 401) {
          setError('Invalid password')
        } else {
          setError(`Authentication failed: ${res.status}`)
        }
        setLoading(false)
        return
      }

      const data = await res.json()
      onLogin(data.token)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Connection failed')
      setLoading(false)
    }
  }

  return (
    <div className="login">
      <div className="login-card">
        <h1>Wisp</h1>
        <p>Remote Access</p>
        <form onSubmit={handleSubmit}>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder="Password"
            disabled={loading}
            autoFocus
          />
          <button type="submit" disabled={loading || !password}>
            {loading ? 'Connecting...' : 'Login'}
          </button>
          {error && <div className="error-message">{error}</div>}
        </form>
      </div>
    </div>
  )
}
