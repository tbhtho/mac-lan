package diagnostics

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestControlReleaseReportKeepsOnlyFixedReasonAndExistingCounters(t *testing.T) {
	report := NewReport("mac", "control-release", "video-stale", map[string]int{"controlActive": 0, "keyCode": 55, "pointerX": 99, "token": 1})
	if report.Event != "control-release" || report.Code != "video-stale" || len(report.Counts) != 1 || report.Counts["controlActive"] != 0 {
		t.Fatalf("fixed release diagnostic was not preserved: %+v", report)
	}
	for _, reason := range []string{"secret-token", "192.168.1.2", "/Users/private-name", "viewer-blur\nsecret-token"} {
		invalid := NewReport("mac", "control-release", reason, nil)
		data, _ := json.Marshal(invalid)
		if invalid.Code != "unknown" || strings.Contains(string(data), reason) {
			t.Fatal("arbitrary release detail entered diagnostics")
		}
	}
}
