package metrics

import (
	"strings"
	"testing"
	"time"
)

func TestRecordCreateIncrementsCounts(t *testing.T) {
	RecordCreate("metrics-create-test", 50*time.Millisecond, "success")

	if !strings.Contains(createCount.String(), `"metrics-create-test"`) {
		t.Errorf("createCount missing profile key after RecordCreate: %s", createCount.String())
	}
	if !strings.Contains(createDuration.String(), `"metrics-create-test"`) {
		t.Errorf("createDuration missing profile key after RecordCreate: %s", createDuration.String())
	}
	assertExpvarContains(t, operationsTotal.String(), `metrics-create-test|create|success`)
}

func TestRecordStopIncrementsCounts(t *testing.T) {
	RecordStop("metrics-stop-test", 30*time.Millisecond, "success")

	if !strings.Contains(stopCount.String(), `"metrics-stop-test"`) {
		t.Errorf("stopCount missing profile key after RecordStop: %s", stopCount.String())
	}
	if !strings.Contains(stopDuration.String(), `"metrics-stop-test"`) {
		t.Errorf("stopDuration missing profile key after RecordStop: %s", stopDuration.String())
	}
	assertExpvarContains(t, operationsTotal.String(), `metrics-stop-test|stop|success`)
}

func TestRecordWorkflowQueuedIncrementsCounter(t *testing.T) {
	RecordWorkflowQueued("octo/wf-queue-test", "ci", "build", "metrics-wf-queue")

	assertExpvarContains(t, workflowQueued.String(), `octo/wf-queue-test|ci|build|metrics-wf-queue`)
}

func TestRecordWorkflowStartedIncrementsCounter(t *testing.T) {
	RecordWorkflowStarted("octo/wf-start-test", "ci", "build", "metrics-wf-start")

	assertExpvarContains(t, workflowStarted.String(), `octo/wf-start-test|ci|build|metrics-wf-start`)
}

func TestRecordWorkflowCompletedIncrementsCounterAndConclusions(t *testing.T) {
	RecordWorkflowCompleted("octo/wf-complete-test", "ci", "build", "metrics-wf-complete", "success")

	assertExpvarContains(t, workflowComplete.String(), `octo/wf-complete-test|ci|build|metrics-wf-complete|success`)
	assertExpvarContains(t, workflowConclusions.String(), `octo/wf-complete-test|ci|build|metrics-wf-complete|success`)
}

func TestRecordWorkflowQueueDurationIgnoresNegativeDuration(t *testing.T) {
	// Use a unique key so we can detect any change from nil state
	key := metricKey("octo/neg-queue-test", "ci", "build", "metrics-neg-queue")
	before := workflowQueueCount.Get(key)

	RecordWorkflowQueueDuration("octo/neg-queue-test", "ci", "build", "metrics-neg-queue", -1*time.Millisecond)

	after := workflowQueueCount.Get(key)
	// Both should be the same (nil if never recorded before, or unchanged count)
	if before != after {
		t.Errorf("negative queue duration should not update counter: before=%v after=%v", before, after)
	}
}

func TestRecordWorkflowRunDurationIgnoresNegativeDuration(t *testing.T) {
	key := metricKey("octo/neg-run-test", "ci", "build", "metrics-neg-run", "success")
	before := workflowRunCount.Get(key)

	RecordWorkflowRunDuration("octo/neg-run-test", "ci", "build", "metrics-neg-run", "success", -1*time.Millisecond)

	after := workflowRunCount.Get(key)
	if before != after {
		t.Errorf("negative run duration should not update counter: before=%v after=%v", before, after)
	}
}

func TestMetricJoinConcatenatesWithPipe(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a|b"},
		{[]string{"a", "b", "c"}, "a|b|c"},
		{[]string{"owner/repo", "ci", "build"}, "owner/repo|ci|build"},
	}
	for _, tt := range tests {
		got := metricJoin(tt.parts...)
		if got != tt.want {
			t.Errorf("metricJoin(%v) = %q, want %q", tt.parts, got, tt.want)
		}
	}
}

func TestMetricKeyReturnsUnquotedJoinedKey(t *testing.T) {
	got := metricKey("a", "b", "c")
	if got != "a|b|c" {
		t.Errorf("metricKey(a,b,c) = %q, want %q", got, "a|b|c")
	}
}

func TestRecordWorkflowQueueDurationAcceptsPositive(t *testing.T) {
	RecordWorkflowQueueDuration("octo/pos-queue-test", "ci", "build", "metrics-pos-queue", 100*time.Millisecond)

	assertExpvarContains(t, workflowQueueCount.String(), `octo/pos-queue-test|ci|build|metrics-pos-queue`)
}

func TestRecordWorkflowRunDurationAcceptsPositive(t *testing.T) {
	RecordWorkflowRunDuration("octo/pos-run-test", "ci", "build", "metrics-pos-run", "success", 200*time.Millisecond)

	assertExpvarContains(t, workflowRunCount.String(), `octo/pos-run-test|ci|build|metrics-pos-run|success`)
}
