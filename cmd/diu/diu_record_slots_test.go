package main

import "testing"

func TestRecorderSlotOwnedByEffectiveUser(t *testing.T) {
	tests := []struct {
		name         string
		uid          uint32
		effectiveUID int64
		isOwned      bool
	}{
		{name: "same user", uid: 12, effectiveUID: 12, isOwned: true},
		{name: "different user", uid: 12, effectiveUID: 13},
		{name: "negative effective UID", effectiveUID: -1},
		{name: "effective UID above uint32", effectiveUID: 1 << 32},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := recorderSlotOwnedByEffectiveUser(test.uid, test.effectiveUID); got != test.isOwned {
				t.Fatalf("owner match = %t, want %t", got, test.isOwned)
			}
		})
	}
}
