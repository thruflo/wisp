import { createStreamDB } from '@durable-streams/state'
import type { SDKMessage } from '@anthropic-ai/claude-agent-sdk'
import { stateSchema } from './schema'

export type { SDKMessage }

export interface CreateDbOptions {
  token: string
  onDisconnect?: (error: Error) => void
}

export function createDb({ token, onDisconnect }: CreateDbOptions) {
  return createStreamDB({
    streamOptions: {
      url: `${window.location.origin}/stream`,
      headers: { Authorization: `Bearer ${token}` },
      onError: (error) => {
        // For connection errors (server gone), trigger disconnect
        // Don't return anything to let the error propagate and close the stream
        if (onDisconnect) {
          onDisconnect(error)
        }
        // Return undefined to propagate the error and stop the stream
        return undefined
      },
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
