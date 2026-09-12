package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func codexEncodingTopLevelKeys(t *testing.T, body []byte) []string {
	t.Helper()
	require.True(t, gjson.ValidBytes(body), string(body))
	var keys []string
	gjson.ParseBytes(body).ForEach(func(key, _ gjson.Result) bool {
		keys = append(keys, key.String())
		return true
	})
	return keys
}

func TestCodexFieldOrderTablesMatchCodexRS(t *testing.T) {
	for name, tc := range map[string]struct {
		want string
		got  []string
	}{
		"responses": {
			"model,instructions,input,tools,tool_choice,parallel_tool_calls,reasoning,store,stream,stream_options,include,service_tier,prompt_cache_key,text,client_metadata,access_programs",
			codexResponsesFieldOrder,
		},
		"compact": {
			"model,input,instructions,tools,parallel_tool_calls,reasoning,service_tier,prompt_cache_key,text,access_programs",
			codexCompactFieldOrder,
		},
		"ws": {
			"type,model,instructions,previous_response_id,input,tools,tool_choice,parallel_tool_calls,reasoning,store,stream,stream_options,include,service_tier,prompt_cache_key,text,generate,client_metadata,access_programs",
			codexWSCreateFieldOrder,
		},
	} {
		t.Run(name, func(t *testing.T) {
			want := strings.Split(tc.want, ",")
			require.Equal(t, want, tc.got, "expected order is independent of the production table")
			parts := make([]string, 0, len(want))
			for i := len(want) - 1; i >= 0; i-- {
				parts = append(parts, fmt.Sprintf("%q:%d", want[i], i))
			}
			body := []byte("{" + strings.Join(parts, ",") + "}")
			out := reorderCodexTopLevelFields(body, tc.got)
			require.Equal(t, want, codexEncodingTopLevelKeys(t, out))
			for i, key := range want {
				require.EqualValues(t, i, gjson.GetBytes(out, key).Int())
			}
		})
	}
}

func TestReorderCodexTopLevelFields(t *testing.T) {
	t.Run("unknown_order_and_raw_values", func(t *testing.T) {
		body := []byte(`{"z":1e+06,"client_metadata":{"b":1.2300,"a":"\u4fdd\u7559 <&>"},"a":9007199254740993,"instructions":"i","m\u006fdel":"m","input":[]}`)
		out := reorderCodexTopLevelFields(body, codexResponsesFieldOrder)
		require.Equal(t, []string{"model", "instructions", "input", "client_metadata", "z", "a"}, codexEncodingTopLevelKeys(t, out))
		for _, key := range []string{"z", "a", "client_metadata"} {
			require.Equal(t, gjson.GetBytes(body, key).Raw, gjson.GetBytes(out, key).Raw)
		}
		require.Contains(t, string(out), `"m\u006fdel":"m"`)
		require.Equal(t, out, reorderCodexTopLevelFields(out, codexResponsesFieldOrder))
	})
	t.Run("invalid_scalar_and_duplicate_are_unchanged", func(t *testing.T) {
		for _, body := range []string{
			"", "not json", "null", "[]", `"scalar"`, "{}", "{broken",
			`{"model":"a","model":"b"}`, `{"model":"a","m\u006fdel":"b"}`,
			`{"input":[],"model":"m",broken}`, `{"input":[],"model":"m"`,
		} {
			require.Equal(t, body, string(reorderCodexTopLevelFields([]byte(body), codexResponsesFieldOrder)), body)
		}
	})
	t.Run("endpoint_and_mode_gates", func(t *testing.T) {
		body := []byte(`{"instructions":"i","input":[],"model":"m"}`)
		for _, mode := range []string{"off", "device", "session", "full"} {
			for _, enabled := range []bool{false, true} {
				account := codexIdentityAccount()
				account.Extra[codexFingerprintModeExtraKey] = mode
				account.Extra[codexFingerprintConvergenceExtraKey] = enabled
				c := codexIdentityHTTPContext("/v1/images/generations", nil)
				for _, target := range []string{"https://example.test/models", "://invalid", "https://example.test/alpha/search"} {
					require.Equal(t, body, applyCodexBodyFieldOrder(c, account, target, body))
				}
				for _, compact := range []bool{false, true} {
					url := "https://example.test/responses"
					want := []string{"model", "instructions", "input"}
					if compact {
						url += "/compact/"
						want = []string{"model", "input", "instructions"}
					}
					out := applyCodexBodyFieldOrder(c, account, url, body)
					if mode == "device" && enabled {
						require.Equal(t, want, codexEncodingTopLevelKeys(t, out))
					} else {
						require.Equal(t, body, out)
					}
				}
			}
		}
	})
}
