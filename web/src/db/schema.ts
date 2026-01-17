import { z } from 'zod'
import { createStateSchema } from '@durable-streams/state'

export const sessionSchema = z.object({
  id: z.string(),
  repo: z.string(),
  branch: z.string(),
  spec: z.string(),
  status: z.enum(['running', 'needs_input', 'blocked', 'done']),
  iteration: z.number(),
  started_at: z.string(),
})

export const taskSchema = z.object({
  id: z.string(),
  session_id: z.string(),
  order: z.number(),
  content: z.string(),
  status: z.enum(['pending', 'in_progress', 'completed']),
})

// SDKMessage schema - passthrough since types come from @anthropic-ai/claude-agent-sdk
// The SDK types (SDKAssistantMessage, SDKResultMessage, etc.) are complex unions.
// Use z.any() and rely on TypeScript for type safety at boundaries.
export const claudeEventSchema = z.object({
  id: z.string(),
  session_id: z.string(),
  iteration: z.number(),
  sequence: z.number(),
  message: z.any(), // SDKMessage from @anthropic-ai/claude-agent-sdk
  timestamp: z.string(),
})

export const inputRequestSchema = z.object({
  id: z.string(),
  session_id: z.string(),
  iteration: z.number(),
  question: z.string(),
  responded: z.boolean(),
  response: z.string().nullable(),
})

export const stateSchema = createStateSchema({
  sessions: { schema: sessionSchema, type: 'session', primaryKey: 'id' },
  tasks: { schema: taskSchema, type: 'task', primaryKey: 'id' },
  claude_events: { schema: claudeEventSchema, type: 'claude_event', primaryKey: 'id' },
  input_requests: { schema: inputRequestSchema, type: 'input_request', primaryKey: 'id' },
})

export type Session = z.infer<typeof sessionSchema>
export type Task = z.infer<typeof taskSchema>
export type ClaudeEvent = z.infer<typeof claudeEventSchema>
export type InputRequest = z.infer<typeof inputRequestSchema>
