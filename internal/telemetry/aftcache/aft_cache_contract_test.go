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
	"errors"
	"slices"
	"testing"

	"github.com/openconfig/gnmi/cache"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// TestAddAFTNotificationZeroTimestampIsStale checks that an atomic update with
// timestamp 0 does not overwrite a newer cached entry. gnmi/cache, which
// aftcache previously relied on, treats 0 as the oldest timestamp and returns
// cache.ErrStale; deletes in aftcache already treat a zero timestamp as oldest.
func TestAddAFTNotificationZeroTimestampIsStale(t *testing.T) {
	const prefix = "198.51.100.1/32"
	c := newAFTCache("dut", "DEFAULT")
	if err := c.addAFTNotification(prefixNotification("DEFAULT", "ipv4-unicast", "ipv4-entry", prefix, 10, 300, true)); err != nil {
		t.Fatalf("addAFTNotification(ts=300) unexpected error: %v", err)
	}
	err := c.addAFTNotification(prefixNotification("DEFAULT", "ipv4-unicast", "ipv4-entry", prefix, 99, 0, true))
	if !errors.Is(err, cache.ErrStale) {
		t.Errorf("addAFTNotification(ts=0) error = %v, want %v", err, cache.ErrStale)
	}
	if got := c.prefixesV4[prefix].nhgID; got != 10 {
		t.Errorf("after ts=0 update, NHG for %s = %d, want 10 (unchanged)", prefix, got)
	}
}

// TestAddAFTNotificationEntryDescendantAtomicPrefix checks the agreed
// "atomic = replace the whole touched entry" contract when an atomic
// notification's prefix is below the entry key (here, a single NHG member):
// the whole NHG is replaced (per the aftcache contract), and the notification is counted
// as a compliance warning because gNMI spec Section 2.1.1 scopes atomicity to
// the notification prefix.
func TestAddAFTNotificationEntryDescendantAtomicPrefix(t *testing.T) {
	c := newAFTCache("dut", "DEFAULT")
	if err := c.addAFTNotification(nhgNotification("DEFAULT", 10, map[uint64]uint64{1: 1, 2: 1}, 100, true)); err != nil {
		t.Fatalf("addAFTNotification(NHG 10 {1,2}) unexpected error: %v", err)
	}
	member := &gnmipb.SubscribeResponse{Response: &gnmipb.SubscribeResponse_Update{Update: &gnmipb.Notification{
		Timestamp: 200,
		Atomic:    true,
		Prefix: &gnmipb.Path{Elem: []*gnmipb.PathElem{
			pathElem("network-instances"), pathElem("network-instance", "name", "DEFAULT"), pathElem("afts"),
			pathElem("next-hop-groups"), pathElem("next-hop-group", "id", "10"),
			pathElem("next-hops"), pathElem("next-hop", "index", "3"),
		}},
		Update: []*gnmipb.Update{
			{Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{pathElem("state"), pathElem("index")}}, Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 3}}},
			{Path: &gnmipb.Path{Elem: []*gnmipb.PathElem{pathElem("state"), pathElem("weight")}}, Val: &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 1}}},
		},
	}}}
	if err := c.addAFTNotification(member); err != nil {
		t.Fatalf("addAFTNotification(member-scoped atomic) unexpected error: %v", err)
	}
	if got := c.nextHopGroups[10].data.NHIDs; !slices.Equal(got, []uint64{3}) {
		t.Errorf("NHG 10 NHIDs = %v, want [3] (whole entry replaced)", got)
	}
	if c.shortPrefixAtomicCount == 0 {
		t.Errorf("compliance warning count = 0, want > 0 for an atomic prefix below the entry key")
	}
}
