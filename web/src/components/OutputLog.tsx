import { useEffect, useRef } from 'react'
import { useLiveQuery, eq } from '@tanstack/react-db'
import type { WispDB, ClaudeEvent, SDKMessage } from '../db/store'

interface OutputLogProps {
  db: WispDB
  sessionId: string
}

export function OutputLog({ db, sessionId }: OutputLogProps) {
  const bottomRef = useRef<HTMLDivElement>(null)

  const events = useLiveQuery((q) =>
    q.from({ events: db.collections.claude_events })
      .where(({ events }) => eq(events.session_id, sessionId))
      .orderBy(({ events }) => events.sequence, 'asc')
  )

  // Auto-scroll to latest output
  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [events.data?.length])

  return (
    <div className="output-log">
      <h3>Output</h3>
      <div className="output-content">
        {events.data?.map((e) => (
          <ClaudeEventRow key={e.id} event={e as ClaudeEvent} />
        ))}
        <div ref={bottomRef} />
      </div>
    </div>
  )
}

// Render based on SDKMessage.type - uses types from @anthropic-ai/claude-agent-sdk
function ClaudeEventRow({ event }: { event: ClaudeEvent }) {
  const msg = event.message as SDKMessage

  switch (msg.type) {
    case 'assistant':
      // msg.message.content is ContentBlock[] (text, tool_use, etc.)
      return (
        <div className="event assistant">
          {(msg as any).message?.content?.map((block: any, i: number) => (
            <ContentBlock key={i} block={block} />
          ))}
        </div>
      )
    case 'user':
      // Tool results from Claude
      return (
        <div className="event tool-result">
          <pre>{JSON.stringify((msg as any).message?.content, null, 2)}</pre>
        </div>
      )
    case 'result':
      return (
        <div className="event result">
          {(msg as any).subtype === 'success'
            ? `Done: ${(msg as any).num_turns} turns, $${(msg as any).total_cost_usd?.toFixed(2) || '?'}`
            : `Error: ${(msg as any).subtype}`}
        </div>
      )
    case 'system':
      return <div className="event system">Session: {(msg as any).session_id}</div>
    default:
      return null
  }
}

// Render content blocks from assistant messages
function ContentBlock({ block }: { block: any }) {
  if (block.type === 'text') {
    return <div className="content-text">{block.text}</div>
  }
  if (block.type === 'tool_use') {
    return (
      <details className="content-tool-use">
        <summary>[{block.name}]</summary>
        <pre>{JSON.stringify(block.input, null, 2)}</pre>
      </details>
    )
  }
  return null
}
