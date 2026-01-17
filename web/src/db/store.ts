import { createStreamDB } from '@durable-streams/state'
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk'
import { stateSchema } from './schema'

export type { SDKMessage }

export function createDb(token: string) {
  return createStreamDB({
    streamOptions: {
      url: '/stream',
      headers: { Authorization: `Bearer ${token}` },
    },
    state: stateSchema,
  })
}

export type WispDB = ReturnType<typeof createDb>
export type ClaudeEvent = {
  id: string
  session_id: string
  iteration: number
  sequence: number
  message: SDKMessage
  timestamp: string
}
