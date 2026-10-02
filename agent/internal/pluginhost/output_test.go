// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package pluginhost_test

import (
	"testing"

	"github.com/Artur-Abalov/sard/agent/internal/pluginhost"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
)

func TestResticErrorsAreWarningsAndTheRestInfo(t *testing.T) {
	cases := map[string]agentv1.LogLevel{
		`{"message_type":"error","error":{"message":"denied"},"during":"archival","item":"/x"}`: agentv1.LogLevel_LOG_LEVEL_WARN,
		`{"message_type":"exit_error","code":1,"message":"Fatal: x"}`:                           agentv1.LogLevel_LOG_LEVEL_WARN,
		"Fatal: unable to open config file":                                                     agentv1.LogLevel_LOG_LEVEL_WARN,
		"/srv/gone does not exist, skipping":                                                    agentv1.LogLevel_LOG_LEVEL_WARN,
		`{"message_type":"verbose_status","action":"new"}`:                                      agentv1.LogLevel_LOG_LEVEL_INFO,
		"signal terminated received, cleaning up":                                               agentv1.LogLevel_LOG_LEVEL_INFO,
		"": agentv1.LogLevel_LOG_LEVEL_INFO,
	}
	for line, want := range cases {
		if got := pluginhost.OutputLevel(line); got != want {
			t.Errorf("OutputLevel(%q) = %v, want %v", line, got, want)
		}
	}
}
