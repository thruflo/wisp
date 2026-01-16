package state

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTask_JSONMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		task Task
		want string
	}{
		{
			name: "incomplete task",
			task: Task{
				Category:    CategoryFeature,
				Description: "Implement user auth",
				Steps:       []string{"Add login form", "Validate credentials"},
				Passes:      false,
			},
			want: `{"category":"feature","description":"Implement user auth","steps":["Add login form","Validate credentials"],"passes":false}`,
		},
		{
			name: "completed task",
			task: Task{
				Category:    CategoryBugfix,
				Description: "Fix null pointer",
				Steps:       []string{"Add nil check"},
				Passes:      true,
			},
			want: `{"category":"bugfix","description":"Fix null pointer","steps":["Add nil check"],"passes":true}`,
		},
		{
			name: "empty steps",
			task: Task{
				Category:    CategorySetup,
				Description: "Initialize project",
				Steps:       []string{},
				Passes:      false,
			},
			want: `{"category":"setup","description":"Initialize project","steps":[],"passes":false}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.task)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestTask_JSONUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    Task
		wantErr bool
	}{
		{
			name:  "valid task",
			input: `{"category":"feature","description":"Add tests","steps":["Write unit tests"],"passes":true}`,
			want: Task{
				Category:    CategoryFeature,
				Description: "Add tests",
				Steps:       []string{"Write unit tests"},
				Passes:      true,
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   `{`,
			want:    Task{},
			wantErr: true,
		},
		{
			name:  "null steps becomes nil slice",
			input: `{"category":"docs","description":"Write docs","steps":null,"passes":false}`,
			want: Task{
				Category:    CategoryDocs,
				Description: "Write docs",
				Steps:       nil,
				Passes:      false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got Task
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVerification_JSONMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		verification Verification
		want         string
	}{
		{
			name: "tests passed",
			verification: Verification{
				Method:  VerificationTests,
				Passed:  true,
				Details: "12 tests passed",
			},
			want: `{"method":"tests","passed":true,"details":"12 tests passed"}`,
		},
		{
			name: "build failed",
			verification: Verification{
				Method:  VerificationBuild,
				Passed:  false,
				Details: "compilation error on line 42",
			},
			want: `{"method":"build","passed":false,"details":"compilation error on line 42"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.verification)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestState_JSONMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state State
		want  string
	}{
		{
			name: "continue status with verification",
			state: State{
				Status:  StatusContinue,
				Summary: "Completed task 3",
				Verification: &Verification{
					Method:  VerificationTests,
					Passed:  true,
					Details: "All tests pass",
				},
			},
			want: `{"status":"CONTINUE","summary":"Completed task 3","verification":{"method":"tests","passed":true,"details":"All tests pass"}}`,
		},
		{
			name: "needs input with question",
			state: State{
				Status:   StatusNeedsInput,
				Summary:  "Need clarification",
				Question: "Should I use REST or GraphQL?",
			},
			want: `{"status":"NEEDS_INPUT","summary":"Need clarification","question":"Should I use REST or GraphQL?"}`,
		},
		{
			name: "blocked with error",
			state: State{
				Status:  StatusBlocked,
				Summary: "Cannot proceed",
				Error:   "Missing dependency: foo",
			},
			want: `{"status":"BLOCKED","summary":"Cannot proceed","error":"Missing dependency: foo"}`,
		},
		{
			name: "done status",
			state: State{
				Status:  StatusDone,
				Summary: "All tasks complete",
			},
			want: `{"status":"DONE","summary":"All tasks complete"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.state)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestState_JSONUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    State
		wantErr bool
	}{
		{
			name:  "valid state with verification",
			input: `{"status":"CONTINUE","summary":"Working on it","verification":{"method":"typecheck","passed":true,"details":"Types are valid"}}`,
			want: State{
				Status:  StatusContinue,
				Summary: "Working on it",
				Verification: &Verification{
					Method:  VerificationTypecheck,
					Passed:  true,
					Details: "Types are valid",
				},
			},
			wantErr: false,
		},
		{
			name:  "minimal state",
			input: `{"status":"DONE","summary":"Complete"}`,
			want: State{
				Status:  StatusDone,
				Summary: "Complete",
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   `{"status":}`,
			want:    State{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got State
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHistory_JSONMarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		history History
		want    string
	}{
		{
			name: "normal iteration",
			history: History{
				Iteration:      5,
				Summary:        "Implemented auth",
				TasksCompleted: 3,
				Status:         StatusContinue,
			},
			want: `{"iteration":5,"summary":"Implemented auth","tasks_completed":3,"status":"CONTINUE"}`,
		},
		{
			name: "first iteration",
			history: History{
				Iteration:      1,
				Summary:        "Set up project",
				TasksCompleted: 1,
				Status:         StatusContinue,
			},
			want: `{"iteration":1,"summary":"Set up project","tasks_completed":1,"status":"CONTINUE"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := json.Marshal(tt.history)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestHistory_JSONUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    History
		wantErr bool
	}{
		{
			name:  "valid history",
			input: `{"iteration":10,"summary":"Finished feature","tasks_completed":7,"status":"DONE"}`,
			want: History{
				Iteration:      10,
				Summary:        "Finished feature",
				TasksCompleted: 7,
				Status:         StatusDone,
			},
			wantErr: false,
		},
		{
			name:    "invalid json",
			input:   `{"iteration": "not a number"}`,
			want:    History{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got History
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskList_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	tasks := []Task{
		{Category: CategorySetup, Description: "Init project", Steps: []string{"Create repo"}, Passes: true},
		{Category: CategoryFeature, Description: "Add auth", Steps: []string{"Design API", "Implement"}, Passes: false},
	}

	data, err := json.MarshalIndent(tasks, "", "  ")
	require.NoError(t, err)

	var got []Task
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, tasks, got)
}

func TestHistoryList_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	history := []History{
		{Iteration: 1, Summary: "Started", TasksCompleted: 1, Status: StatusContinue},
		{Iteration: 2, Summary: "Progress", TasksCompleted: 2, Status: StatusContinue},
		{Iteration: 3, Summary: "Done", TasksCompleted: 3, Status: StatusDone},
	}

	data, err := json.MarshalIndent(history, "", "  ")
	require.NoError(t, err)

	var got []History
	err = json.Unmarshal(data, &got)
	require.NoError(t, err)
	assert.Equal(t, history, got)
}
