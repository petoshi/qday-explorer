package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
)

func TestConnectionCountFromNode(t *testing.T) {
	manifest := chain.QdayDevnet()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/network-status" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"network":     manifest.Network.Name,
			"connections": 42,
		})
	}))
	a := app{
		manifest:      manifest,
		nodeStatusURL: server.URL + "/api/network-status",
		nodeHTTP:      server.Client(),
	}
	if got := a.connectionCount(context.Background()); got != 42 {
		t.Fatalf("got %d connections, want 42", got)
	}
	server.Close()
	if got := a.connectionCount(context.Background()); got != 42 {
		t.Fatalf("lost cached connection count: got %d, want 42", got)
	}
}

func TestNodeSupply(t *testing.T) {
	manifest := chain.QdayDevnet()
	var invalid atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/supply" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		current := amount{QDAY: "458", Atomic: types.Siacoins(458).ExactString()}
		if invalid.Load() {
			current = amount{QDAY: "459", Atomic: types.Siacoins(459).ExactString()}
		}
		_ = json.NewEncoder(w).Encode(supplyStatus{
			Network:             manifest.Network.Name,
			Height:              10,
			Synced:              true,
			UnitAtomic:          types.HastingsPerSiacoin.ExactString(),
			IssuedSupply:        amount{QDAY: "508", Atomic: types.Siacoins(508).ExactString()},
			BurnedSupply:        amount{QDAY: "50", Atomic: types.Siacoins(50).ExactString()},
			CurrentSupply:       current,
			ImmatureSupply:      amount{QDAY: "8", Atomic: types.Siacoins(8).ExactString()},
			CirculatingSupply:   amount{QDAY: "450", Atomic: types.Siacoins(450).ExactString()},
			MaximumIssuedSupply: amount{QDAY: "8500000", Atomic: types.Siacoins(8_500_000).ExactString()},
			UnspentOutputs:      3,
		})
	}))
	defer server.Close()
	a := app{manifest: manifest, nodeStatusURL: server.URL + "/api/network-status", nodeHTTP: server.Client()}
	supply, err := a.nodeSupply(context.Background())
	if err != nil {
		t.Fatal(err)
	} else if supply.CirculatingSupply.QDAY != "450" || supply.Height != 10 {
		t.Fatalf("wrong supply: %+v", supply)
	}
	invalid.Store(true)
	if _, err := a.nodeSupply(context.Background()); err == nil {
		t.Fatal("accepted inconsistent node supply")
	}
}

func TestFormatAmount(t *testing.T) {
	unit := types.HastingsPerSiacoin
	for _, test := range []struct {
		value types.Currency
		want  string
	}{
		{types.ZeroCurrency, "0"},
		{types.Siacoins(8), "8"},
		{unit.Div64(1000), "0.001"},
		{types.Siacoins(8).Add(unit.Div64(1000)), "8.001"},
	} {
		if got := formatAmount(test.value, unit); got != test.want {
			t.Fatalf("formatAmount(%v): got %q, want %q", test.value, got, test.want)
		}
	}
}

func TestTransactionKind(t *testing.T) {
	for _, test := range []struct {
		kind byte
		want string
	}{
		{consensus.QdayTransfer, "TRANSFER"},
		{consensus.QdayCoinbase, "MINER MARKER"},
		{consensus.QdayCanaryProof, "QDAY PROOF"},
	} {
		txn := types.V2Transaction{ArbitraryData: (consensus.QdayEnvelope{Kind: test.kind}).Encode()}
		if got, _ := transactionKind(txn); got != test.want {
			t.Fatalf("kind %d: got %q, want %q", test.kind, got, test.want)
		}
	}
	txn := types.V2Transaction{
		ArbitraryData:  (consensus.QdayEnvelope{Kind: consensus.QdayTransfer}).Encode(),
		SiacoinOutputs: []types.SiacoinOutput{{Address: types.VoidAddress, Value: types.Siacoins(10)}},
	}
	if got, _ := transactionKind(txn); got != "BURN" {
		t.Fatalf("void transfer classified as %q", got)
	}
	summary := summarizeTransaction(txn, nil, time.Time{}, 0, types.HastingsPerSiacoin, true)
	if summary.Kind != "BURN" || summary.To != "BURN" || summary.Value.QDAY != "10" {
		t.Fatalf("wrong burn summary: %+v", summary)
	}
}
