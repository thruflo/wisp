import { useParams, Navigate } from 'react-router-dom'
import { useLiveQuery, eq } from '@tanstack/react-db'
import type { WispDB } from '../db/store'
import type { Actions } from '../db/actions'
import { TaskList } from './TaskList'
import { OutputLog } from './OutputLog'
import { InputPrompt } from './InputPrompt'

interface SessionProps {
  db: WispDB
  actions: Actions
}

export function Session({ db, actions }: SessionProps) {
  const { id } = useParams<{ id: string }>()

  const sessionQuery = useLiveQuery((q) =>
    q.from({ sessions: db.collections.sessions })
      .where(({ sessions }) => eq(sessions.id, id!))
      .limit(1)
  )

  const inputRequests = useLiveQuery((q) =>
    q.from({ requests: db.collections.input_requests })
      .where(({ requests }) => eq(requests.session_id, id!))
      .where(({ requests }) => eq(requests.responded, false))
      .orderBy(({ requests }) => requests.iteration, 'desc')
      .limit(1)
  )

  const session = sessionQuery.data?.[0]
  const activeRequest = inputRequests.data?.[0]

  if (!id) {
    return <Navigate to="/" replace />
  }

  if (!session) {
    return (
      <div className="session-loading">
        <p>Loading session...</p>
      </div>
    )
  }

  return (
    <div className="session">
      <header className="session-header">
        <div className="session-info">
          <h2>{session.repo}</h2>
          <p className="branch">{session.branch}</p>
        </div>
        <div className="session-status">
          <span className={`status-badge ${session.status}`}>
            {session.status}
          </span>
          <span className="iteration">Iteration {session.iteration}</span>
        </div>
      </header>

      <div className="session-content">
        <aside className="session-sidebar">
          <TaskList db={db} sessionId={id} />
        </aside>

        <section className="session-main">
          {activeRequest && (
            <InputPrompt
              db={db}
              actions={actions}
              request={activeRequest}
            />
          )}
          <OutputLog db={db} sessionId={id} />
        </section>
      </div>
    </div>
  )
}
