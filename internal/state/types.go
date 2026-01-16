package state

// Task represents a single implementation task from tasks.json.
type Task struct {
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Steps       []string `json:"steps"`
	Passes      bool     `json:"passes"`
}

// Verification describes how a task was verified.
type Verification struct {
	Method  string `json:"method"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

// State represents the agent's current state from state.json.
type State struct {
	Status       string        `json:"status"`
	Summary      string        `json:"summary"`
	Question     string        `json:"question,omitempty"`
	Error        string        `json:"error,omitempty"`
	Verification *Verification `json:"verification,omitempty"`
}

// History represents a single iteration record in history.json.
type History struct {
	Iteration      int    `json:"iteration"`
	Summary        string `json:"summary"`
	TasksCompleted int    `json:"tasks_completed"`
	Status         string `json:"status"`
}

// Status values for State.Status field.
const (
	StatusContinue   = "CONTINUE"
	StatusDone       = "DONE"
	StatusNeedsInput = "NEEDS_INPUT"
	StatusBlocked    = "BLOCKED"
)

// Verification method values.
const (
	VerificationTests     = "tests"
	VerificationTypecheck = "typecheck"
	VerificationBuild     = "build"
	VerificationManual    = "manual"
	VerificationNone      = "none"
)

// Task category values.
const (
	CategorySetup    = "setup"
	CategoryFeature  = "feature"
	CategoryBugfix   = "bugfix"
	CategoryRefactor = "refactor"
	CategoryTest     = "test"
	CategoryDocs     = "docs"
)
