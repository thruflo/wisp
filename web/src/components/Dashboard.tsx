import { Link } from 'react-router-dom'
import { useLiveQuery } from '@tanstack/react-db'
import type { WispDB } from '../db/store'

interface DashboardProps {
  db: WispDB
}

export function Dashboard({ db }: DashboardProps) {
  const sessions = useLiveQuery((q) =>
    q.from({ sessions: db.collections.sessions })
      .orderBy(({ sessions }) => sessions.started_at, 'desc')
  )

  return (
    <div className="dashboard">
      <h2>Sessions</h2>
      {sessions.data?.length === 0 ? (
        <p className="no-sessions">No active sessions</p>
      ) : (
        <div className="session-list">
          {sessions.data?.map((session) => (
            <Link
              key={session.id}
              to={`/session/${session.id}`}
              className={`session-card status-${session.status}`}
            >
              <div className="session-repo">{session.repo}</div>
              <div className="session-branch">{session.branch}</div>
              <div className="session-meta">
                <span className={`status-badge ${session.status}`}>
                  {session.status}
                </span>
                <span className="iteration">Iteration {session.iteration}</span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </div>
  )
}
