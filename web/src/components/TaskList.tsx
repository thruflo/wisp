import { useLiveQuery, eq } from '@tanstack/react-db'
import type { WispDB } from '../db/store'

interface TaskListProps {
  db: WispDB
  sessionId: string
}

export function TaskList({ db, sessionId }: TaskListProps) {
  const tasks = useLiveQuery((q) =>
    q.from({ tasks: db.collections.tasks })
      .where(({ tasks }) => eq(tasks.session_id, sessionId))
      .orderBy(({ tasks }) => tasks.order, 'asc')
  )

  return (
    <div className="task-list">
      <h3>Tasks</h3>
      {tasks.data?.length === 0 ? (
        <p className="no-tasks">No tasks yet</p>
      ) : (
        <ul>
          {tasks.data?.map((task) => (
            <li key={task.id} className={`task status-${task.status}`}>
              <span className="task-status-icon">
                {task.status === 'completed' && '✓'}
                {task.status === 'in_progress' && '→'}
                {task.status === 'pending' && '○'}
              </span>
              <span className="task-content">{task.content}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
