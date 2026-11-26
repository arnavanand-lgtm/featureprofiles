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
	"fmt"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// buildPrefixScaleCache returns a cache holding n IPv4 /32 prefixes (all
// pointing at NHG 10 -> NH 1) and the matching want set.
func buildPrefixScaleCache(tb testing.TB, n int) (*aftCache, map[string]bool) {
	tb.Helper()
	c := newAFTCache("dut", "DEFAULT")
	if err := c.addAFTNotification(nhNotification("DEFAULT", 1, "192.0.2.2", "Ethernet1", 100, true)); err != nil {
		tb.Fatalf("addAFTNotification(NH 1) unexpected error: %v", err)
	}
	if err := c.addAFTNotification(nhgNotification("DEFAULT", 10, map[uint64]uint64{1: 1}, 100, true)); err != nil {
		tb.Fatalf("addAFTNotification(NHG 10) unexpected error: %v", err)
	}
	want := make(map[string]bool, n)
	resp := prefixNotification("DEFAULT", "ipv4-unicast", "ipv4-entry", "", 10, 100, true)
	prefixKey := resp.GetUpdate().GetPrefix().GetElem()[4].GetKey()
	prefixVal := resp.GetUpdate().GetUpdate()[0].GetVal().Value.(*gnmipb.TypedValue_StringVal)
	statePrefixVal := resp.GetUpdate().GetUpdate()[1].GetVal().Value.(*gnmipb.TypedValue_StringVal)
	for i := 0; i < n; i++ {
		// 100.64.0.0/10 (RFC 6598 shared address space), used only as unique keys.
		prefix := fmt.Sprintf("100.%d.%d.%d/32", 64+((i>>16)&0x3f), (i>>8)&0xff, i&0xff)
		want[prefix] = true
		prefixKey["prefix"], prefixVal.StringVal, statePrefixVal.StringVal = prefix, prefix, prefix
		if err := c.addAFTNotification(resp); err != nil {
			tb.Fatalf("addAFTNotification(%q) unexpected error: %v", prefix, err)
		}
	}
	return c, want
}

// readViewForBenchmark is a benchmark-only sketch of an in-package read helper:
// it lets periodic hooks read the cached maps under RLock without ToAFT's full
// copy. f must not retain references after it returns.
func (c *aftCache) readViewForBenchmark(f func(v4, v6 map[string]prefixRecord)) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	f(c.prefixesV4, c.prefixesV6)
}

var benchmarkSink int

// BenchmarkPeriodicHookCost compares what the periodic hooks do today (a full
// ToAFT copy, then lookups) with the same lookups done through a read view.
//
//	go test -run='^$' -bench=PeriodicHookCost -benchmem ./internal/telemetry/aftcache/
func BenchmarkPeriodicHookCost(b *testing.B) {
	for _, n := range []int{200_000, 2_000_000} {
		c, want := buildPrefixScaleCache(b, n)
		ss := &AFTStreamSession{Cache: c}
		countViaView := func() int {
			got := 0
			c.readViewForBenchmark(func(v4, v6 map[string]prefixRecord) {
				for p := range want {
					if _, ok := v4[p]; ok {
						got++
					} else if _, ok := v6[p]; ok {
						got++
					}
				}
			})
			return got
		}

		b.Run(fmt.Sprintf("ToAFT/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a, err := ss.ToAFT(nil, nil)
				if err != nil {
					b.Fatalf("ToAFT() unexpected error: %v", err)
				}
				benchmarkSink = len(a.Prefixes)
			}
		})
		// Same work as DeletionStoppingCondition today: copy, then count.
		b.Run(fmt.Sprintf("DeletionCheck/copy/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a, err := ss.ToAFT(nil, nil)
				if err != nil {
					b.Fatalf("ToAFT() unexpected error: %v", err)
				}
				remaining := 0
				for p := range a.Prefixes {
					if want[p] {
						remaining++
					}
				}
				benchmarkSink = remaining
			}
		})
		b.Run(fmt.Sprintf("DeletionCheck/view/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkSink = countViaView()
			}
		})
		// Same work as the "Check Prefixes" stage of InitialSyncStoppingCondition today.
		b.Run(fmt.Sprintf("PrefixPresence/copy/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				a, err := ss.ToAFT(nil, nil)
				if err != nil {
					b.Fatalf("ToAFT() unexpected error: %v", err)
				}
				got := 0
				for p := range want {
					if _, ok := a.Prefixes[p]; ok {
						got++
					}
				}
				benchmarkSink = got
			}
		})
		b.Run(fmt.Sprintf("PrefixPresence/view/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkSink = countViaView()
			}
		})
	}
}
