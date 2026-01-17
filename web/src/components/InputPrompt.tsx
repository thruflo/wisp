import { useState, type FormEvent, useEffect } from 'react'
import type { WispDB } from '../db/store'
import type { Actions } from '../db/actions'
import type { InputRequest } from '../db/schema'

interface InputPromptProps {
  db: WispDB
  actions: Actions
  request: InputRequest
}

export function InputPrompt({ actions, request }: InputPromptProps) {
  const [value, setValue] = useState('')

  // Request notification permission and show notification when needed
  useEffect(() => {
    if (!request.responded && document.hidden) {
      requestNotificationPermission().then(() => {
        showNotification('Wisp needs input', request.question)
      })
    }
  }, [request.id, request.responded, request.question])

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault()
    if (!value.trim()) return
    actions.submitInput({ requestId: request.id, response: value })
    setValue('')
  }

  if (request.responded) {
    return (
      <div className="input-prompt responded">
        <p className="question">{request.question}</p>
        <p className="response">Responded: {request.response}</p>
      </div>
    )
  }

  return (
    <div className="input-prompt">
      <form onSubmit={handleSubmit}>
        <p className="question">{request.question}</p>
        <div className="input-row">
          <input
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="Type your response..."
            autoFocus
          />
          <button type="submit" disabled={!value.trim()}>
            Send
          </button>
        </div>
      </form>
    </div>
  )
}

async function requestNotificationPermission(): Promise<boolean> {
  if (!('Notification' in window)) {
    return false
  }
  if (Notification.permission === 'granted') {
    return true
  }
  if (Notification.permission !== 'denied') {
    const permission = await Notification.requestPermission()
    return permission === 'granted'
  }
  return false
}

function showNotification(title: string, body: string): void {
  if (Notification.permission === 'granted') {
    const notification = new Notification(title, { body })
    notification.onclick = () => {
      window.focus()
      notification.close()
    }
  }
}
