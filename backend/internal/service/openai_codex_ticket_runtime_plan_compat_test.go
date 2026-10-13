package service

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodexTicketRuntimePlanRulesStayWithTheirServiceSnapshot(t *testing.T) {
	makeService := func(length string) *OpenAIGatewayService {
		repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{
			SettingKeyOpenAICodexTicketDefaultLength: "292",
			SettingKeyOpenAICodexTicketPlanLengths:   `[{"plan":"team","length":` + length + `}]`,
		}}}
		svc := ticketTestService(t, config.OpenAICodexTicketConfig{TargetLength: 292}, nil)
		svc.settingService = NewSettingService(repo, &config.Config{})
		return svc
	}
	first := makeService("332")
	second := makeService("316")
	account := ticketTestAccount(41)
	account.Credentials["plan_type"] = "team"

	firstConfig := first.openAICodexTicketConfig()
	secondConfig := second.openAICodexTicketConfig()
	require.Equal(t, 332, openAICodexTicketTargetLength(account, firstConfig))
	require.Equal(t, 316, openAICodexTicketTargetLength(account, secondConfig))
	require.Equal(t, 332, openAICodexTicketTargetLength(account, firstConfig))
}
