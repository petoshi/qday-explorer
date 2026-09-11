package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
}
