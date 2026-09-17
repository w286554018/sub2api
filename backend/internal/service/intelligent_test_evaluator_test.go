package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExactAnswerEvaluatorRequiresAnswerLine(t *testing.T) {
	evaluation := exactAnswerEvaluator{}.Evaluate("24 - 6 = 18; half leaves 9; plus 3.\nANSWER: 12", IntelligentTestConfig{ExpectedAnswer: "12"})
	require.Equal(t, IntelligentTestStatusSuccess, evaluation.Status)
	require.Equal(t, "correct", evaluation.Detail["answer_verdict"])
	require.NotNil(t, evaluation.Score)
	require.Equal(t, 100.0, *evaluation.Score)
}

func TestExactAnswerEvaluatorClassifiesWrongAnswerAsSuspectedDegradation(t *testing.T) {
	evaluation := exactAnswerEvaluator{}.Evaluate("ANSWER: 9", IntelligentTestConfig{ExpectedAnswer: "12"})
	require.Equal(t, IntelligentTestStatusSuspectedDegradation, evaluation.Status)
	require.Equal(t, "incorrect", evaluation.Detail["answer_verdict"])
}

func TestSanitizeIntelligentTestSVGRejectsActiveContent(t *testing.T) {
	_, err := SanitizeIntelligentTestSVG(`<svg viewBox="0 0 10 10"><script>alert(1)</script><circle r="4" cx="5" cy="5"/></svg>`)
	require.Error(t, err)

	safe, err := SanitizeIntelligentTestSVG(`<svg viewBox="0 0 10 10"><circle r="4" cx="5" cy="5" fill="red"/></svg>`)
	require.NoError(t, err)
	require.Contains(t, safe, `<circle`)
	require.NotContains(t, safe, `<script`)
}

func TestIntelligentReadOnlyAccountRepoSuppressesAccountStateWrites(t *testing.T) {
	repo := intelligentReadOnlyAccountRepo{}
	require.NoError(t, repo.SetError(context.Background(), 1, "failed"))
	require.NoError(t, repo.SetRateLimited(context.Background(), 1, time.Now()))
	require.NoError(t, repo.ClearTempUnschedulable(context.Background(), 1))
	require.Error(t, repo.Update(context.Background(), &Account{}))
	require.Error(t, repo.BindGroups(context.Background(), 1, []int64{2}))
}
