// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package aftcache

import (
	"sync"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// newFailingNHSession returns a session whose only prefix resolves to next hop
// 192.0.2.2, so next-hop validation against wantFailingNHs always fails.
func newFailingNHSession(t *testing.T, target string) *AFTStreamSession {
	t.Helper()
	c := newAFTCache(target, "DEFAULT")
	for i, n := range []*gnmipb.SubscribeResponse{
		nhNotification("DEFAULT", 1, "192.0.2.2", "Ethernet1", 100, true),
		nhgNotification("DEFAULT", 10, map[uint64]uint64{1: 1}, 100, true),
		prefixNotification("DEFAULT", "ipv4-unicast", "ipv4-entry", "198.51.100.1/32", 10, 100, true),
	} {
		if err := c.addAFTNotification(n); err != nil {
			t.Fatalf("addAFTNotification(seed[%d]) unexpected error: %v", i, err)
		}
	}
	return &AFTStreamSession{
		Cache:             c,
		start:             time.Now(),
		missingPrefixes:   make(map[string]bool),
		failingNHPrefixes: make(map[string]bool),
	}
}

var (
	failingNHWantPrefixes = map[string]bool{"198.51.100.1/32": true}
	wantFailingNHs        = map[string]bool{"192.0.2.99": true}
)

// TestInitialSyncStoppingConditionFailBudgetIsPerSession checks that the
// next-hop failure budget (20 tries) is counted per session. Tests such as
// afts_base pass the same stopping-condition hook to two ListenUntil
// sessions; with a shared counter the two sessions exhaust the budget together
// after ~10 periodic checks each, i.e. well before the convergence timeout.
func TestInitialSyncStoppingConditionFailBudgetIsPerSession(t *testing.T) {
	sessions := []*AFTStreamSession{newFailingNHSession(t, "dut1"), newFailingNHSession(t, "dut2")}
	hook := InitialSyncStoppingCondition(t, nil, failingNHWantPrefixes, wantFailingNHs, nil)
	for tick := 1; tick < 20; tick++ { // 19 failing checks per session stay within a per-session budget of 20.
		for i, ss := range sessions {
			done, err := hook.PeriodicFunc(ss)
			if err != nil {
				t.Fatalf("tick %d, session %d: PeriodicFunc() error = %v, want nil (failure budget appears shared across sessions)", tick, i+1, err)
			}
			if done {
				t.Fatalf("tick %d, session %d: PeriodicFunc() done = true, want false (next hops never match)", tick, i+1)
			}
		}
	}
}

// TestInitialSyncStoppingConditionConcurrentSessionsRace calls one
// stopping-condition hook from two goroutines, the way afts_base's fetchAFT
// drives two sessions. It only fails when run with the race detector
// (go test -race), which reports the unsynchronized shared failure counter.
// Errors from PeriodicFunc are ignored here: the shared budget is covered by
// TestInitialSyncStoppingConditionFailBudgetIsPerSession.
func TestInitialSyncStoppingConditionConcurrentSessionsRace(t *testing.T) {
	sessions := []*AFTStreamSession{newFailingNHSession(t, "dut1"), newFailingNHSession(t, "dut2")}
	hook := InitialSyncStoppingCondition(t, nil, failingNHWantPrefixes, wantFailingNHs, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, ss := range sessions {
		wg.Add(1)
		go func(ss *AFTStreamSession) {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				hook.PeriodicFunc(ss) //nolint:errcheck // see function comment.
			}
		}(ss)
	}
	close(start)
	wg.Wait()
}
