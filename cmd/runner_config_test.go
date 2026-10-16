// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package main

import "testing"

func TestDecodeRunnerSettings(t *testing.T) {
	for _, test := range []struct {
		name, data string
	}{
		{"native", `{"agentId":9007199254740993,"poolId":1,"ephemeral":true,"useV2Flow":true,"disableUpdate":true,"skipSessionRecover":false,"IsHostedServer":false}`},
		{"jit", `{"AgentId":"9007199254740993","PoolId":"1","Ephemeral":"True","UseV2Flow":"True","DisableUpdate":"True","SkipSessionRecover":"False","IsHostedServer":"False","PoolName":null}`},
		{"mixed", `{"agentId":"9007199254740993","poolId":1,"ephemeral":"true","useV2Flow":true,"disableUpdate":"true","skipSessionRecover":false,"IsHostedServer":false}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			settings, err := decodeRunnerSettings([]byte(test.data))
			if err != nil {
				t.Fatal(err)
			}
			if settings.AgentID != 9007199254740993 || settings.PoolID != 1 || !settings.Ephemeral || !settings.UseV2Flow || !settings.DisableUpdate || settings.SkipSessionRecover || settings.HostedServer == nil || *settings.HostedServer {
				t.Fatalf("incorrect settings: %+v", settings)
			}
		})
	}
	settings, err := decodeRunnerSettings([]byte(`{"AgentId":"1","IsHostedServer":null}`))
	if err != nil || settings.AgentID != 1 || settings.HostedServer != nil {
		t.Fatalf("nullable hosted-server setting: %+v, %v", settings, err)
	}
	for _, data := range []string{
		`{"AgentId":"not-a-number"}`,
		`{"AgentId":"18446744073709551616"}`,
		`{"AgentId":"-1"}`,
		`{"AgentId":"1.5"}`,
		`{"PoolId":"invalid"}`,
		`{"Ephemeral":"invalid"}`,
		`{"UseV2Flow":42}`,
	} {
		if _, err := decodeRunnerSettings([]byte(data)); err == nil {
			t.Errorf("accepted invalid settings: %s", data)
		}
	}
}
