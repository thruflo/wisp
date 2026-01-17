// Package server provides a web server for remote monitoring and interaction
// with wisp sessions.
//
// The server enables developers to monitor and interact with active wisp sessions
// from any device (phone, tablet, another computer) while away from the machine
// running wisp. It serves a React web client and exposes endpoints for
// authentication, state streaming via Durable Streams, and user input.
//
// # Endpoints
//
//   - POST /auth - Password authentication, returns session token
//   - GET /stream - Durable Streams endpoint for real-time state updates
//   - POST /input - Submit user response to NEEDS_INPUT prompts
//   - GET / - Serve embedded web assets (React client)
//
// # Authentication
//
// The server uses password-based authentication with argon2id hashing.
// Clients POST their password to /auth and receive a session token that
// must be included in subsequent requests via the Authorization header.
package server
