package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Keep the fork's global Excel / BPS switch configurable after upstream sync.
func TestUpdateSettingsPreservesExcelBPSSwitch(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{"excel_bps_enabled": "true"})

	rec := doUpdateSettings(t, h, map[string]any{"available_channels_enabled": true, "excel_bps_enabled": false}, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "false", repo.values["excel_bps_enabled"])
	var resp struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	var enabled bool
	require.NoError(t, json.Unmarshal(resp.Data["excel_bps_enabled"], &enabled))
	require.False(t, enabled)
}
