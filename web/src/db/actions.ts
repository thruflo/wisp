import { createOptimisticAction } from '@tanstack/react-db'
import type { WispDB } from './store'

export function createActions(db: WispDB, token: string) {
  const submitInput = createOptimisticAction<{ requestId: string; response: string }>({
    onMutate: ({ requestId, response }) => {
      // Instant optimistic update
      db.collections.input_requests.update(requestId, (draft) => {
        draft.responded = true
        draft.response = response
      })
    },
    mutationFn: async ({ requestId, response }) => {
      // POST to server
      const res = await fetch('/input', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ request_id: requestId, response }),
      })
      if (!res.ok) {
        throw new Error(`Failed to submit input: ${res.status}`)
      }
      // No refetch needed - server sends confirmed state via stream
    },
  })

  return { submitInput }
}

export type Actions = ReturnType<typeof createActions>
